package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"app/internal/help"
	"app/internal/update"
)

// ---- 软件更新 ----
//
// 后端能力全在 internal/update（ed25519 签名校验、版本比较、下载、解压、
// 替换、重启），这里只把它接成端点，供设置屏与启动检查使用。
//
// 三点不能含糊：
//
//  1. **未配置清单地址就完全关闭**。cfg.UpdateURL 为空是默认状态（本地部署
//     不需要联网检查），此时不能发任何网络请求，也不能弹任何提示。
//
//  2. **更新必须用户确认后才会下载并替换 exe**。静默自动更新会直接覆盖
//     用户正在运行的程序——Fyne 版是 dialog.ShowConfirm 交互式的，这里沿用
//     同样的语义：端点只负责"查"和"下载"，"应用"要单独一步且带显式确认。
//
//  3. **不按角色区分**（2026-10-15）：三个端点对所有登录角色开放，check 甚至
//     允许未登录（登录页要能提示）。这不是疏忽，见 server.go 里接线处的注释：
//     边界是"包必须由生产私钥签名"这道与角色无关的闸门，而不是"谁能点按钮"。

type updateChecker struct {
	once sync.Once
	url  string
	cur  string
	err  error
	c    *update.Checker
}

func (s *Server) updateURL() string { return s.updater.url }

// logUpdateProgress 每收到约 5% 就记一行下载进度。
//
// 步长从**实际拿到的 total** 算，而不是清单里的 Size：清单缺 size 时 Size==0，
// 而 `done % (0/20+1)` 恒为 0，于是每个 32KB 分片都打一行日志——几十兆的包能刷出
// 上千行，把真正有用的启动日志淹掉。
func logUpdateProgress(start time.Time, done, total int64) {
	step := int64(0)
	if total > 0 {
		step = total / 20
		if step <= 0 {
			step = 1
		}
	}
	if step <= 0 || done%step != 0 {
		return
	}
	log.Printf("update download: %d/%d bytes (%s)", done, total, time.Since(start).Round(time.Second))
}

func (s *Server) checkerFor(url string) (*update.Checker, error) {
	s.updater.once.Do(func() {
		build := s.newChecker
		if build == nil {
			build = update.NewChecker
		}
		s.updater.c, s.updater.err = build(url, s.updater.cur)
	})
	return s.updater.c, s.updater.err
}

// handleCheckUpdate 查询是否有新版本。不下载、不改任何东西。
func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	url := s.updater.url
	if url == "" {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	checker, err := s.checkerFor(url)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "更新检查不可用: "+err.Error())
		return
	}
	res, err := checker.Check()
	if err != nil {
		// 这里的 err 已经过 preferError 挑选：若某个源的清单验签被拒，报的是那一条，
		// 而不是回退源的连接失败（否则一次发版事故会看起来像网络抖动）。
		writeErr(w, http.StatusBadGateway, "无法获取更新信息: "+err.Error())
		return
	}
	m := res.Manifest
	out := map[string]any{
		"enabled":        true,
		"currentVersion": s.updater.cur,
		"hasUpdate":      m != nil,
	}
	// 有源"验签被拒 / 清单不可用"时，即使净结果是"没有更新"，也要把这件事带出去。
	// 否则界面只能说"已是最新版本"，发版签错密钥这类事故就完全不可见（先决项⑤）。
	if warn := res.Warning(); warn != "" {
		out["warning"] = warn
	}
	if m != nil {
		out["version"] = m.Version
		out["notes"] = m.Notes
		// 已切好小节的正文，和 /api/help 的更新说明页签用同一个解析器
		// （help.ParseNotes）。前端不再自己按 `== 中文 ==` 切一遍：同一份 notes
		// 在两个界面上切出不同段落，迟早变成「弹窗里少一节、设置里多一节」。
		out["sections"] = helpSections(help.ParseNotes(m.Notes))
		out["size"] = m.Size
		out["publishedAt"] = m.PublishedAt
		out["channel"] = m.Channel
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDownloadUpdate 下载（不应用）。
//
// 两种模式：
//   - **同步**（默认，不带 ?async=1）：下载完才回响应。给脚本、测试、以及不需要
//     进度条的调用方用；
//   - **异步**（?async=1）：立刻回 202 与一个 job id，下载在后台跑，前端用
//     GET /api/update/progress 轮询进度。
//
// 为什么需要异步：同步模式下整个下载期间 HTTP 连接一直被占着，前端既拿不到中间
// 进度（只能看到转圈），也不能中途取消；而 UI 上"正在下载 x%"是用户判断该不该
// 等着的唯一依据（一个 30MB 的包在慢网络上要好几分钟）。
// 代价是多一个端点和一个 job 的生命周期——所以只在明确要进度时才走异步。
// ---- 异步下载任务 ----
//
// 只为一个目的存在：让界面能显示"正在下载 x%"。同步模式下 HTTP 连接被占住，
// 前端既看不到进度也不能取消，而一个几十 MB 的包在慢网络上要等好几分钟，
// 用户只能盯着一个转圈图标猜它是不是卡住了。

// jobKeepDuration 是下载完成后任务记录在内存里多留多久。
//
// 多留这一会儿是为了容错：前端轮询可能刚好在完成的那一刻断线/超时，重连后
// 还能拿到"已完成"而不是"任务不存在"——否则用户会看到刚要成功就变成报错。
// 过期记录由下一次查询顺手清掉，不起定时器（一个进程里的更新任务，
// 不值得为它养一个 goroutine）。
const jobKeepDuration = 5 * time.Minute

// maxJobs 是内存里最多保留几个任务记录。单个用户同一时刻只会有一个下载，
// 留 4 个足够覆盖"刚完成又发起下一次"这类情况；超过就丢最旧的。
const maxJobs = 4

var (
	errSuperseded = errors.New("下载任务被新的下载取代")
)

type downloadResult struct {
	path    string
	version string
	err     error
}

// downloadJob 是一次后台下载。
//
// done 是一次性通道：finish 关掉它，于是每个等它的进度请求都能立刻醒过来
// （否则它们要各自轮询 finished 标志，白白多一轮延迟）。
type downloadJob struct {
	id      string
	version string

	mu        sync.Mutex
	doneBytes int64
	total     int64
	finished  bool
	res       downloadResult
	at        time.Time

	done chan struct{}
}

func newDownloadJob(id, version string, total int64) *downloadJob {
	return &downloadJob{
		id:      id,
		version: version,
		total:   total,
		done:    make(chan struct{}),
		at:      time.Now(),
	}
}

func (j *downloadJob) finish(res downloadResult) {
	j.mu.Lock()
	if j.finished {
		j.mu.Unlock()
		return
	}
	j.finished = true
	j.res = res
	if res.path != "" {
		j.doneBytes = j.total
	}
	j.mu.Unlock()
	close(j.done)
}

// jobState 是一次快照：进度数字 + 是否已完成 + 完成后的结果。
//
// **所有**从 job 读出来的字段都必须经它取出（在锁内拷贝），不能在 handler 里直接
// 读 job 的字段：进度回调在后台 goroutine 里持续写这些字段，直接读会被 -race 抓到。
// 上一版就是 `"total": job.total` 这么直接读的，本地跑没带 -race 所以没暴露，
// CI 一跑就红。
type jobState struct {
	doneBytes int64
	total     int64
	finished  bool
	res       downloadResult
}

func (j *downloadJob) snapshot() jobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return jobState{
		doneBytes: j.doneBytes,
		total:     j.total,
		finished:  j.finished,
		res:       j.res,
	}
}

func (j *downloadJob) expired(now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.finished && now.Sub(j.at) > jobKeepDuration
}

// setJob 登记一个新任务，返回被顶掉的旧任务（可能为 nil）。
// 同一时刻只保留一个"正在跑"的下载。
//
// 先插入再剪枝，保证 job 表永不超过 maxJobs 个。
func (s *Server) setJob(job *downloadJob) *downloadJob {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	prev := s.jobs[job.id]
	s.jobs[job.id] = job
	s.pruneJobsLocked(time.Now())
	return prev
}

func (s *Server) job(id string) *downloadJob {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	s.pruneJobsLocked(time.Now())
	return s.jobs[id]
}

// pruneJobsLocked 丢掉已过期与超额的记录。调用方必须持 jobsMu。
func (s *Server) pruneJobsLocked(now time.Time) {
	for id, j := range s.jobs {
		if j.expired(now) {
			delete(s.jobs, id)
		}
	}
	for len(s.jobs) > maxJobs {
		oldest, oldestID := time.Time{}, ""
		for id, j := range s.jobs {
			if oldestID == "" || j.at.Before(oldest) {
				oldest, oldestID = j.at, id
			}
		}
		delete(s.jobs, oldestID)
	}
}

// newDownloadJobID 生成一个短的 job id。
//
// 用时间戳+计数而不是随机数：它是进程内唯一标识，不对外暴露也不需要密码学强度
// （能猜到别人的 job id 也只能看自己的下载进度——apply 要的是 pendingUpdate 那一个）。
func (s *Server) newDownloadJobID() string {
	s.jobSeqMu.Lock()
	defer s.jobSeqMu.Unlock()
	s.jobSeq++
	return fmt.Sprintf("%d-%d", time.Now().UnixMilli(), s.jobSeq)
}

func (s *Server) handleDownloadUpdate(w http.ResponseWriter, r *http.Request) {
	url := s.updater.url
	if url == "" {
		writeErr(w, http.StatusBadRequest, "未配置更新地址")
		return
	}
	checker, err := s.checkerFor(url)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "更新检查不可用: "+err.Error())
		return
	}
	res, err := checker.Check()
	if err != nil || res.Manifest == nil {
		// 把"为什么没有可下载的新版本"说清楚：如果原因是某个源的清单验签被拒，
		// 那和"确实已是最新"是两件事，不能让调用方以为后者。
		msg := "没有可下载的新版本"
		if warn := res.Warning(); warn != "" {
			msg += "（更新源清单未通过校验：" + warn + "）"
		}
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	m := res.Manifest

	if r.URL.Query().Get("async") == "1" {
		s.startAsyncDownload(w, checker, m)
		return
	}

	zipPath, err := checker.Download(m, os.TempDir(), func(done, total int64) {
		// 同步模式：进度只写日志（前端拿不到，见上面的说明）。
		logUpdateProgress(time.Now(), done, total)
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "下载失败: "+err.Error())
		return
	}
	// 记住「刚下载并通过校验的那一个」。apply 只认它，客户端不再有机会指认路径。
	// sha 用清单里的值：downloadOnce 已经核对过文件与它一致。
	s.setPendingUpdate(zipPath, m.SHA256)
	writeJSON(w, http.StatusOK, map[string]any{"path": zipPath, "version": m.Version})
}

// startAsyncDownload 后台跑下载，立刻回 202 与 job id。
func (s *Server) startAsyncDownload(w http.ResponseWriter, checker *update.Checker, m *update.Manifest) {
	job := newDownloadJob(s.newDownloadJobID(), m.Version, m.Size)
	if prev := s.setJob(job); prev != nil {
		// 上一个 job 还在跑：不是错误，但必须让它收敛，否则它的 done 永远不关闭。
		prev.finish(downloadResult{err: errSuperseded})
	}

	go func() {
		start := time.Now()
		zipPath, err := checker.Download(m, os.TempDir(), func(done, total int64) {
			job.mu.Lock()
			job.doneBytes = done
			if total > 0 {
				job.total = total
			}
			job.mu.Unlock()
			logUpdateProgress(start, done, total)
		})
		if err != nil {
			job.finish(downloadResult{err: err})
			return
		}
		// 只有校验通过的文件才登记为"待应用"：下载失败绝不能留下一个能被 apply 的槽位。
		s.setPendingUpdate(zipPath, m.SHA256)
		job.finish(downloadResult{path: zipPath, version: m.Version})
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"jobId":   job.id,
		"version": m.Version,
		"total":   m.Size,
	})
}

// handleUpdateProgress 查询异步下载的进度。
//
// 返回三种状态：running（有 done/total）、done（path 可 apply）、error（原因）。
// 任务记录在下载完成后再留一会儿，让刚好在前端重连的进度请求还能拿到结果；
// 过期由 pruneJobs 顺手清掉，不做定时器。
func (s *Server) handleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("job")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "缺少 job 参数")
		return
	}
	job := s.job(id)
	if job == nil {
		writeErr(w, http.StatusNotFound, "下载任务不存在或已过期")
		return
	}
	res := job.snapshot()
	out := map[string]any{
		"jobId":   job.id,
		"version": job.version,
		"done":    res.doneBytes,
		"total":   res.total,
		"state":   "running",
	}
	if res.finished {
		switch {
		case res.res.err != nil:
			out["state"] = "error"
			out["error"] = res.res.err.Error()
		default:
			out["state"] = "done"
			out["path"] = res.res.path
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// setPendingUpdate 记录待应用的更新包。
func (s *Server) setPendingUpdate(path, sha string) {
	s.pendingUpdate.mu.Lock()
	defer s.pendingUpdate.mu.Unlock()
	s.pendingUpdate.path = path
	s.pendingUpdate.sha = sha
}

// takePendingUpdate 读取待应用的更新包。none 为 true 表示没有下载过。
func (s *Server) takePendingUpdate() (path, sha string, none bool) {
	s.pendingUpdate.mu.Lock()
	defer s.pendingUpdate.mu.Unlock()
	return s.pendingUpdate.path, s.pendingUpdate.sha, s.pendingUpdate.path == ""
}

// clearPendingUpdate 清掉待应用的更新包（应用成功后调用）。
func (s *Server) clearPendingUpdate() {
	s.pendingUpdate.mu.Lock()
	defer s.pendingUpdate.mu.Unlock()
	s.pendingUpdate.path = ""
	s.pendingUpdate.sha = ""
}

// handleApplyUpdate 解压替换 exe 并重启。
//
// **只能用「下载步骤刚产生并校验过」的那个更新包**：请求体里的 path 一律忽略。
//
// 这一条是 2026-10-13 审计后改的。此前它直接拿请求体的 path 交给
// update.ApplyAndRestart，而那条链是「从该路径的 zip 里取出任意 .exe →
// 覆盖程序自身 → 启动它」，且 apply 阶段不验签。于是一个已登录会话只要目标机器上
// 存在一个 zip 且知道路径，就能让 ERP 以自己身份运行攻击者的程序（随之可读
// config.json、连数据库、读写全部备份）。当时这条路由还挂着 System.ApplyUpdate
// 权限点，非管理员碰不到；该权限点已于 2026-10-15 随「更新对齐旧版」删除，
// 因此下面这条"路径不可指认"成了唯一的路径约束。
//
// 原设计里「下载时验过 SHA-256」是对的，但**验证绑在下载产生的临时文件上，
// 与 apply 传进来的路径毫无关联** —— 等于把校验和"将执行的文件"割开了。
// 现在两者是同一个对象：路径由服务端记住，apply 前再核一次哈希，
// 连"下载与 apply 之间被换文件"这个窗口也一并关掉。
//
// 2026-10-15 起这条端点对所有登录角色开放（见 server.go 接线处的注释），
// 所以"路径不可由客户端指认"从"多一层保险"变成了**唯一的路径约束**：权限矩阵
// 不再能拦住任意路径的 zip，只有这里能。
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		// Path 保留仅为线协议兼容，**不再使用**。前端仍会回传下载得到的路径，
		// 但服务端只认自己记住的那一个。
		Path    string `json:"path"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 显式确认仍然要。它是「下载」和「动手」之间给**用户**看的闸门；
	// 真正拦住路径替换的是下面那条因果链，不是这个布尔值。
	if !in.Confirm {
		writeErr(w, http.StatusBadRequest, "需要显式确认才能替换程序文件")
		return
	}

	zipPath, wantSHA, none := s.takePendingUpdate()
	if none {
		writeErr(w, http.StatusBadRequest, "尚未下载更新包，请先下载")
		return
	}
	// apply 之前再核对一次哈希：验证过的文件与即将执行的文件必须是同一个。
	gotSHA, err := update.FileSHA256(zipPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "更新包已不可读取，请重新下载")
		return
	}
	if !strings.EqualFold(gotSHA, wantSHA) {
		// 文件被换过了。作废这个待办，逼用户重新走一次下载（下载会重新验签）。
		s.clearPendingUpdate()
		writeErr(w, http.StatusBadRequest, "更新包内容与下载时不一致，已拒绝应用，请重新下载")
		return
	}

	if err := s.applyUpdate(zipPath); err != nil {
		// 失败**不清**待办：文件还在，用户可以直接重试，不必重新下载。
		writeErr(w, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
	s.clearPendingUpdate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})

	// 换完 exe 之后**必须让旧进程退出**，否则更新等于没做（2026-10-15 查出，Round 8 先决项⑥）。
	//
	// update.ApplyAndRestart 只负责启动新进程，文档里明写"caller 应随后退出"。不退的后果：
	// 旧进程继续持有单实例锁，新进程带着 RFERP_UPDATE_RESTART=1 等满 20 秒后弹
	// "已在运行"并自己退出——用户点了安装，exe 却没被换掉。原生弹框那条路早就有
	// os.Exit(0)（Fyne 版同位置也有），只有这个 HTTP 端点漏了。
	//
	// 为什么延迟而不是立刻退：响应还没送达客户端，进程就没了的话前端会看到
	// "restarting" 变成网络错误，白白让人以为失败。延后退出期间旧进程仍在服务，
	// 已经拿到响应的客户端不会受影响。
	s.exitAfterRestart()

	// 把"要退出了"这件事说清楚：这条响应之后客户端再发任何请求都可能落空，
	// 日志里留下痕迹比什么都不写更容易排查。
	log.Printf("update: 已换新版本，%s 后退出本进程（pid %d）", exitAfterResponseDelay, os.Getpid())
}

// exitAfterRestart 在短暂延迟后退出当前进程，让刚启动的新版本接管单实例锁。
//
// 单独一个方法而不是内联 os.Exit：测试里它是可注入的（WithProcessExit），
// 否则跑测试会把自己的测试二进制换掉 —— 与 applyUpdate 同一个理由。
func (s *Server) exitAfterRestart() {
	go func() {
		// 延迟要够 HTTP 响应真正出去。200ms 是实测：本地环回写一个 30 字节的 JSON
		// 远不到这个量级，留 5 倍余量；太长则会让"点了没反应"的感觉变明显。
		time.Sleep(exitAfterResponseDelay)
		s.exitProcess()
	}()
}

// exitAfterResponseDelay 是「回完响应」到「退出旧进程」之间的等待。
// 只给 exitAfterRestart 用：别的等待各有各的理由（单飞节流有自己的门），
// 复用这个常量会让「为什么是 200ms」失去出处。
const exitAfterResponseDelay = 200 * time.Millisecond
