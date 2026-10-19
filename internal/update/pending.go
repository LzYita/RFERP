package update

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// 更新确认标记（Round 8 先决项④：更新失败也要能回退）
//
// 问题：换完 exe 之后，磁盘上唯一的回退副本是 RFERP.exe.old。而它原先在新进程
// 启动早期就被删掉，判据只是"进程跑到了清理那一行"，不是"新版本可用"。于是新版本
// 若起不来（配置损坏、库连不上、依赖缺失），用户既没有能用的新版本，也没有旧版本，
// 只能自己去安装目录抢救。
//
// 协议：
//
//	1. 换 exe 之前写标记（ApplyAndRestart 负责）；
//	2. 新进程启动时 PrepareStart 看到 .old 与标记 → 保留 .old；
//	3. **界面真的挂载起来**之后调 ConfirmHealthy：清标记、清 .old、清 .failed；
//	4. 标记在、.old 也在，说明上一轮始终没走到界面可用 → 这次启动主动问用户要不要回退。
//
// 标记放在 exe 同目录：更新能成功替换 exe 就说明该目录可写，不需要另找位置；
// 而且回退是"相对这个 exe"的事，放一起才不会走散。
//
// **为什么确认条件是"界面挂载"而不是"进程活着"**：新版最典型的坏法就是前端资源
// 出问题——窗口照开、进程照活，用户看到的是一片空白。拿"进程跑了 5 秒"当健康，
// 正好把这种情况判成健康，从此再也不会有人被问起回退。所以健康由前端主动上报
// （POST /api/startup/ui-ready），上报不到就一直不确认。
//
// **为什么 1.3.0 升上来也要能回退**：1.3.0（Fyne）的 ApplyAndRestart 只做重命名，
// 不写标记（那时还没有这套协议）。于是"有 .old、无标记"不是"没有待确认的更新"，
// 而是"从不会写标记的旧版升上来的"——那恰恰是最需要回退的一次跨越。PrepareStart
// 见到这种组合就补记标记。

const (
	// pendingFile 是标记文件名，与 exe 同目录。
	pendingFile = "RFERP.update-pending"
	// oldSuffix 是回退副本的后缀（与 ApplyAndRestart 保持一致）。
	oldSuffix = ".old"
	// failedSuffix 是回退时被换下来的坏 exe，留着供排查，下次成功启动时清理。
	failedSuffix = ".failed"
)

// Pending 是标记文件的内容。只记时间：够用来写日志与判断"是否上一轮留下的"，
// 不记版本号是为了让 ApplyAndRestart 不必知道版本（它拿不到，调用方才拿得到）。
type Pending struct {
	At time.Time `json:"at"`
}

// exePath 返回当前 exe 的真实路径（解析符号链接，与 ApplyAndRestart 一致）。
//
// 是变量而不是函数，只为测试能指向临时目录：标记与 .old 都按 exe 同目录定位，
// 而测试进程的 exe 在 Go 的临时目录里，不能真的往那儿写。生产代码从不改它。
var exePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func pendingPath(exe string) string { return filepath.Join(filepath.Dir(exe), pendingFile) }

// writePending 在替换 exe **之前**写下标记。
//
// 写在前面而不是后面：如果进程在"写完标记"与"换完 exe"之间死掉，留下的标记只会
// 让下一次启动多保留一个 .old（无害）；反过来先换后写，中间死掉就没有标记，
// .old 会被当成过期文件删掉 —— 那正是要避免的方向。
func writePending(exe string) error {
	data, err := json.Marshal(Pending{At: time.Now().UTC()})
	if err != nil {
		return err
	}
	return os.WriteFile(pendingPath(exe), data, 0o644)
}

// ReadPending 读标记；不存在时返回 (nil, nil)。
func ReadPending() (*Pending, error) {
	exe, err := exePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(pendingPath(exe))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		// 标记坏了不能当成"没有标记"：那样会删掉唯一的回退副本。
		// 当成"存在但内容不可读"返回，调用方会保守地保留 .old。
		return &Pending{}, fmt.Errorf("更新标记内容无法解析: %w", err)
	}
	return &p, nil
}

// ClearPending 删除标记。只删标记，不动 .old —— 什么时候删 .old 由下一次启动决定。
func ClearPending() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	if err := os.Remove(pendingPath(exe)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ConfirmHealthy 表示"新版本确实把界面显示出来了"：清标记，清回退副本，
// 清回退时留下的坏版本。
//
// **确认就删 .old**，而不是"留到下一次启动再删"。理由是标记已清零之后 .old 就没有
// 任何用途了：offerUpdateRollback 要先读到标记才会问，标记没了就永远问不到它 ——
// 那份 18 MB 只是一块谁也不会再被指到的死文件。删掉它同时让"补记标记"这件事
// 天然只发生一次：没有 .old 就没有可补记的东西。
func ConfirmHealthy() {
	if err := ClearPending(); err != nil {
		log.Printf("update: 清除更新标记失败: %v", err)
	}
	exe, err := exePath()
	if err != nil {
		return
	}
	for _, p := range []string{exe + oldSuffix, exe + failedSuffix} {
		if rmErr := os.Remove(p); rmErr == nil {
			log.Printf("update: 已清理 %s", filepath.Base(p))
		} else if !os.IsNotExist(rmErr) {
			log.Printf("update: 清理 %s 失败: %v", filepath.Base(p), rmErr)
		}
	}
}

// StartDecision 是 PrepareStart 的结论：这次启动该怎么处理上一轮的残留。
type StartDecision struct {
	// Copy 表示磁盘上有 .old 可回退。
	Copy bool
	// Unconfirmed 表示当前 exe 还没被证明界面可用（标记在，或刚被补记）。
	Unconfirmed bool
	// OfferRollback 表示**现在就**该问用户要不要回退。
	//
	// 只有"上一轮已经启动过、却始终没走到界面可用"才为真。刚从旧版升上来
	// （标记是这次补记的）**不算**——那还没有任何失败证据，此时问等于
	// 每次跨架构升级都先吓用户一跳。
	OfferRollback bool
}

// PrepareStart 在启动早期处理上一轮更新的残留，并给出这次该怎么办。
//
// 三种情况：
//  1. 没有 .old → 没有可回退的东西，什么也不做（残留的标记是空壳：没有副本
//     可指，offerUpdateRollback 会因 HasRollbackCopy 为假而不问）；
//  2. 有 .old、没有标记 → 从不会写标记的旧版升上来的，**补记**标记；
//  3. 有 .old、有标记 → 上一轮始终没走到界面可用 → 该问用户了。
//
// 标记读不出来时保留 .old 但不主动问：不知道上一轮到底有没有真的启动过，
// 贸然问会打扰用得好好的用户；真起不来时，失败路径自己会问。
func PrepareStart() StartDecision {
	exe, err := exePath()
	if err != nil {
		log.Printf("update: 无法定位 exe，保留回退副本: %v", err)
		return StartDecision{}
	}
	oldPath := exe + oldSuffix
	if _, err := os.Stat(oldPath); err != nil {
		return StartDecision{}
	}
	d := StartDecision{Copy: true, Unconfirmed: true}

	p, err := ReadPending()
	if err != nil {
		log.Printf("update: 更新标记不可读，保留回退副本且不主动询问: %v", err)
		return d
	}
	if p != nil {
		log.Printf("update: 上次更新自 %s 起始终未被确认，保留回退副本", p.At.Format(time.RFC3339))
		d.OfferRollback = true
		return d
	}
	// 有 .old 却无标记：1.3.0 那样的旧版升上来的第一次启动。
	if err := writePending(exe); err != nil {
		log.Printf("update: 补记更新标记失败（仍保留回退副本）: %v", err)
		return d
	}
	log.Printf("update: 发现 %s 但没有更新标记，按跨架构升级补记", filepath.Base(oldPath))
	return d
}

// HasRollbackCopy 报告 .old 是否存在：先确认有得回退，再问用户要不要回退。
// 问了却没有回退副本，比不问更糟。
func HasRollbackCopy() bool {
	exe, err := exePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(exe + oldSuffix)
	return err == nil
}

// Rollback 把上一版本的 exe 换回来，返回换回来的路径。
//
// 不做任何询问，也不启动新进程：要不要回退、回退之后怎么提示，是调用方（界面）
// 的决定。这里只保证磁盘状态正确，并且**保留被替换下来的坏 exe**（.failed），
// 否则排查时连"坏在哪一版"都没了。
func Rollback() (string, error) {
	exe, err := exePath()
	if err != nil {
		return "", err
	}
	oldPath := exe + oldSuffix
	if _, err := os.Stat(oldPath); err != nil {
		return "", fmt.Errorf("没有可回退的上一版本（%s 不存在）: %w", filepath.Base(oldPath), err)
	}
	failedPath := exe + failedSuffix
	if err := os.Remove(failedPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("无法清理上一次的 %s: %w", filepath.Base(failedPath), err)
	}
	// Windows 允许重命名正在运行的 exe（同 ApplyAndRestart 里那一步），
	// 但不允许覆盖正在运行的文件，所以先把它挪开再放回旧的。
	if err := os.Rename(exe, failedPath); err != nil {
		return "", fmt.Errorf("无法移走当前版本: %w", err)
	}
	if err := os.Rename(oldPath, exe); err != nil {
		// 换回失败：把当前版本放回去，至少不要变成"两个都没有"。
		if backErr := os.Rename(failedPath, exe); backErr != nil {
			log.Printf("update: 回退失败且无法还原当前版本: %v", backErr)
		}
		return "", fmt.Errorf("无法放回上一版本: %w", err)
	}
	// 回退成功后标记必须消失，否则下一次启动会以为还有一次未确认的更新。
	if err := ClearPending(); err != nil {
		log.Printf("update: 回退后清除标记失败: %v", err)
	}
	log.Printf("update: 已回退到上一版本（当前版本保留为 %s）", filepath.Base(failedPath))
	return exe, nil
}
