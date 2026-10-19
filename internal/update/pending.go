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
//	2. 新进程启动时看到标记 → 说明上一轮没确认过 → **不删** .old；
//	3. 窗口起来并稳定一段时间后删标记（只删标记）；
//	4. 下一次启动若没有标记 → 说明上一轮确认过了 → 这时才删 .old。
//
// 也就是说 .old 至少活过一次完整启动。最坏情况（新 exe 根本起不来）下它一直在，
// 用户或支持人员可以把 .old 改名回 RFERP.exe 恢复。
//
// 标记放在 exe 同目录：更新能成功替换 exe 就说明该目录可写，不需要另找位置；
// 而且回退是"相对这个 exe"的事，放一起才不会走散。

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

// ConfirmHealthy 表示"新版本确实起来了"：清掉标记，并清理回退时留下的坏版本。
//
// 只清标记、不删 .old 是刻意的：此刻程序才刚起来，多留一次启动的回退余地
// 比省 18 MB 值得。而 .failed 可以删 —— 一个能跑起来的版本已经在运行，
// 被换下来的坏版本只在回退后那段时间内有排查价值。
func ConfirmHealthy() {
	if err := ClearPending(); err != nil {
		log.Printf("update: 清除更新标记失败: %v", err)
	}
	if exe, err := exePath(); err == nil {
		if rmErr := os.Remove(exe + failedSuffix); rmErr == nil {
			log.Printf("update: 已清理回退时留下的 %s", filepath.Base(exe+failedSuffix))
		} else if !os.IsNotExist(rmErr) {
			log.Printf("update: 清理 %s 失败: %v", filepath.Base(exe+failedSuffix), rmErr)
		}
	}
}

// CleanupStale 清理上一次更新留下的过期回退副本（.old）。
//
// **只有在没有标记时才该调用**：有标记说明当前 exe 还没被确认过，.old 是唯一
// 的回退副本，删掉就等于放弃回退。这条约束由调用方保证，这里再挡一道。
//
// 不管 .failed：它是回退时的诊断材料，什么时候清由 ConfirmHealthy 决定 ——
// 一个能跑起来的版本出现之后才不需要它。
func CleanupStale() {
	p, err := ReadPending()
	if err != nil {
		// 标记读不出来/坏了：保守保留，不清理。
		log.Printf("update: 更新标记不可读，跳过清理（保留回退副本）: %v", err)
		return
	}
	if p != nil {
		log.Printf("update: 上次更新尚未确认（%s），保留回退副本", p.At.Format(time.RFC3339))
		return
	}
	exe, err := exePath()
	if err != nil {
		return
	}
	path := exe + oldSuffix
	if err := os.Remove(path); err == nil {
		log.Printf("update: 已清理过期的 %s", filepath.Base(path))
	} else if !os.IsNotExist(err) {
		log.Printf("update: 清理 %s 失败: %v", filepath.Base(path), err)
	}
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
