package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"app/internal/backuplayout"
	"app/internal/paths"
	"app/internal/usecase"
)

// 备份 / 恢复 / 清空 / 数据目录的端点。
//
// 这一组端点集中在这里而不是塞进 admin.go，是因为它们有一条共同的、不显眼却很重要的
// 约束：**任何响应都不得包含服务器端绝对路径**。备份目录形如
// D:\仓库数据\备份\，回给客户端等于泄露 Windows 用户名与目录布局——§4.5 缺口 5 记的
// 就是这个问题（POST /api/backup 此前直接返回 {path: "D:\仓库数据\备份\backup_x.sql"}）。
//
// 因此：客户端**只按文件名**指认备份文件，服务端在备份目录内解析；列表也只给文件名。

// backupEntry 是列表里的一项。只含文件名、大小与时间，没有完整路径。
//
// 后三个字段是「预检结论」，可缺失：宿主没实现 usecase.BackupInspector 时整段省略，
// 前端据此显示「无法确认」而不是假装覆盖度没问题。
type backupEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`

	// Category 是物理分类（manual / auto / safety / legacy），界面据此分组。
	Category string `json:"category"`
	// Legacy=true 表示它还在 备份\ 根下（旧布局），分组时排在最后。
	Legacy bool `json:"legacy,omitempty"`

	// SchemaKnown=false 表示预检只读到文件开头、没读到结尾，无法断言覆盖度。
	SchemaKnown bool `json:"schemaKnown,omitempty"`
	// MissingTables 是「当前业务表里、这份备份 schema 中没有」的表：
	// 恢复时它们会被 DELETE 掉且无处回填。SchemaKnown 为 false 时本字段无意义。
	MissingTables []string `json:"missingTables,omitempty"`
	// Restorable=false 时该文件不能作为恢复来源（典型：非 UTF-8 编码），
	// 界面应禁用它并显示 UnusableReason。
	Restorable     bool   `json:"restorable,omitempty"`
	UnusableReason string `json:"unusableReason,omitempty"`
}

// handleListBackups 列出可用于恢复的备份文件。
//
// 恢复界面需要「先选文件、再确认」，而文件在服务器目录里、WebView2 没有原生的
// 本地目录对话框（Fyne 版是 filepath.Walk 自己列的，ui/backup.go:332-370），
// 所以这里提供列表端点。只回文件名：客户端不该知道服务器目录长什么样。
//
// 顺带做**预检**：每份备份能不能安全回滚（覆盖度）与能不能用（编码），
// 在这里就告诉界面，而不是等用户输完确认口令、点完恢复才失败。
func (s *Server) handleListBackups(w http.ResponseWriter, _ *http.Request) {
	entries, err := backuplayout.List()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"files": []backupEntry{}})
		return
	}
	out := make([]backupEntry, 0, len(entries))
	for _, e := range entries {
		entry := backupEntry{
			Name:     e.Name,
			Size:     e.Size,
			Modified: e.ModTime.Format("2006-01-02 15:04:05"),
			Category: string(e.Category),
			Legacy:   e.Legacy,
		}
		// 预检是增强：宿主没实现这个端口就跳过，界面按「无法确认」处理。
		if insp, ok := s.apps.(usecase.BackupInspector); ok {
			if target, err := backuplayout.Resolve(e.Name); err == nil {
				if got, err := insp.InspectBackupFile(target); err == nil {
					entry.SchemaKnown = got.SchemaKnown
					entry.MissingTables = got.MissingTables
					entry.Restorable = got.Usable
					entry.UnusableReason = got.Reason
				}
			}
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

// handleBackupDatabase 生成一份备份，并以**文件流**返回。
//
// 此前返回 {"path": "<服务器绝对路径>"}：浏览器拿不到内容（阶段 B/C 的"下载到本机"
// 在能力矩阵里标的是可用，但代码里没有任何下载能力），同时把服务器目录布局泄了出去。
// 现在直接回内容 + Content-Disposition，浏览器一次请求就完成下载。
func (s *Server) handleBackupDatabase(w http.ResponseWriter, _ *http.Request) {
	// 用户点「一键备份」= 手动备份，落 manual/ 目录。
	// 它**永不自动删除**：用户主动留的东西不该被任何清理策略替用户决定。
	// 自动备份由后台按周期生成，落 auto/ 并滚动保留，与手动严格分开。
	dir, err := backuplayout.DirFor(paths.PrefixManual)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "备份失败，请检查数据目录是否可写")
		return
	}
	path, err := s.apps.BackupDatabase(dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "备份失败，请检查数据目录是否可写")
		return
	}
	// 备份落盘之后立刻回内容；文件本身保留在备份目录里（撤销备份是用户自己的事）。
	f, err := os.Open(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "备份文件无法读取")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		"attachment; filename*=UTF-8''"+urlEscape(filepath.Base(path)))
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		// 此时响应头已发出，改不了状态码；只记日志。
		return
	}
}

// handleRestoreDatabase 整库恢复。
//
// 两道服务端闸门（界面上还有两道，但那不是安全边界）：
//  1. **本机形态**：整库恢复覆盖全部数据，四道确认门建立在"操作者就是这台机器的
//     主人"之上，跨机时只剩仪式（§4.5 能力矩阵）。
//  2. **文件名而非路径**：只接受备份目录内的纯文件名。
func (s *Server) handleRestoreDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperations(w, "整库恢复") {
		return
	}
	var body struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	target, err := s.resolveBackupFile(body.File)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.apps.RestoreDatabase(target)
	if res.RequiresRestart {
		s.restoreRequiresRestart.Store(true)
		// 整文件回退也回退账号身份，旧的记住登录不能跨库继续使用。
		s.remember.mu.Lock()
		clear(s.remember.byID)
		s.remember.mu.Unlock()
		s.remember.clearDisk()
	}
	if err != nil {
		if res.RequiresRestart {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "SQLite 恢复结果未确认，请退出并重新打开程序后核对数据，勿直接重试", "state": "unknown"})
			return
		}
		writeDestructiveErr(w, "restore", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"requiresRestart": res.RequiresRestart,
		"statements":      res.Statements,
		"tableCounts":     res.TableCounts,
		// 只给文件名：用户要退回时从备份列表里选它。
		"preRestore": safetyCopyName(res.PreRestore),
	})
}

func (s *Server) handleExitAfterRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperations(w, "恢复后退出") {
		return
	}
	if !s.restoreRequiresRestart.Load() {
		writeErr(w, http.StatusConflict, "没有需要重启的恢复操作")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "exiting"})
	s.exitAfterRestart()
}

// handleClearDatabase 清空业务数据（账号、迁移记录与数据库身份保留）。
func (s *Server) handleClearDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperations(w, "清空业务数据") {
		return
	}
	// 端口与"回滚口令"都在 body 里；口令由界面那道门负责采集，服务端独立复核，
	// 否则绕过界面直接调端点就能清库。
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if body.Confirm != ClearConfirmToken {
		// 逐字比对，**不 TrimSpace**。Fyne 版也是 `!= "drop"`（ui/backup.go:248，
		// 且 backup_gates_test.go:170 断言空格口令也拒）。TrimSpace 会把口令
		// 降格成"随便打几个字符"——它本该证明操作者读懂了后果。
		writeErr(w, http.StatusBadRequest, "确认口令不正确，未执行清空")
		return
	}
	res, err := s.apps.ClearDatabase()
	if err != nil {
		writeDestructiveErr(w, "clear", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cleared":  res.Cleared,
		"preClear": safetyCopyName(res.PreClear),
	})
}

// handlePickDataDir 请求宿主弹一个**原生**目录选择框。
//
// 这条端点存在的意义是让原生能力穿过 HTTP 这条线到达前端，而前端始终不知道
// 自己跑在桌面上（见 DirectoryPicker 的说明）。没注入 picker 的部署形态
// （cmd/server、浏览器）会得到一个明确的"不支持"而不是 500——界面据此把
// 文本输入框亮出来，而不是让用户对着一个点不动的按钮发呆。
// handleGetAutoBackup 返回自动定期备份的设置与状态。
//
// 只读、仓管也可见（和 DataDir 同档）：让用户能看到「上次自动备份是什么时候」，
// 是判断这套东西有没有在工作的唯一依据。
func (s *Server) handleGetAutoBackup(w http.ResponseWriter, _ *http.Request) {
	c, ok := s.apps.(usecase.AutoBackupController)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false, "days": 0, "count": 0,
			"keep": paths.AutoBackupKeep, "reason": "当前部署形态不支持自动备份",
		})
		return
	}
	writeJSON(w, http.StatusOK, c.AutoBackupStatus())
}

// handleSetAutoBackup 设置自动备份间隔。
//
// 本机操作（写配置 + 触发备份都发生在这台机器上），所以跟 SetDataDir 一样
// 挂在 hostOperations 闸门后面 —— 远程客户端改本机的备份策略没有意义。
func (s *Server) handleSetAutoBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperations(w, "设置自动备份") {
		return
	}
	c, ok := s.apps.(usecase.AutoBackupController)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "当前部署形态不支持自动备份")
		return
	}
	var body struct {
		Days int `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := c.SetAutoBackupDays(body.Days); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 立刻落一份：用户刚开启就该看到结果，而不是等下个周期才发现它没工作。
	// 失败不回滚设置——设置是用户的意图，后台备份失败只记日志。
	if body.Days > 0 {
		if insp, ok := s.apps.(usecase.AutoBackupScheduler); ok {
			if _, err := insp.MaybeAutoBackup(time.Now()); err != nil {
				log.Printf("api: auto backup after enabling: %v", err)
			}
		}
	}
	writeJSON(w, http.StatusOK, c.AutoBackupStatus())
}

func (s *Server) handlePickDataDir(w http.ResponseWriter, _ *http.Request) {
	// 选目录本身不改任何东西，权限与本机闸门挂在真正的写操作 SetDataDir 上。
	// 但既然它是为"改数据目录"服务的，就跟着 Backup.SetDataDir 的档位要求，
	// 免得只读账号能弹出框却提交不了。
	if !s.requireHostOperations(w, "更改数据目录") {
		return
	}
	if s.picker == nil {
		writeErr(w, http.StatusNotImplemented, "当前服务无法弹出目录选择框，请手动输入路径")
		return
	}
	dir, err := s.picker.PickDirectory(s.apps.DataDir())
	if err != nil {
		// 弹框失败（WebView2 异常等）是宿主的问题，不是用户的输入问题，
		// 回 500 让界面提示"手动输入"，并把原因写进日志。
		log.Printf("api: pick data dir: %v", err)
		writeErr(w, http.StatusInternalServerError, "目录选择框无法打开，请手动输入路径")
		return
	}
	if dir == "" {
		// 用户取消是正常结果，用 200 + 空串表达，界面保持原状。
		writeJSON(w, http.StatusOK, map[string]any{"dir": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir})
}

// handleDataDir 读取数据目录。
//
// 只读操作，仓管也有权限（Backup.DataDir 是 read 档），所以**不**受本机闸门限制——
// 仓管在备份屏要看得到备份/导出放在哪儿。
func (s *Server) handleDataDir(w http.ResponseWriter, _ *http.Request) {
	dir := s.apps.DataDir()
	writeJSON(w, http.StatusOK, map[string]any{
		// 目录本身是服务器端路径。但界面上必须显示它——否则用户不知道备份去哪了、
		// 也不知道该去哪里取回恢复文件。这是"展示给操作者本人"，与"跨机回传路径给
		// 任意调用方"是两回事：本端点已鉴权且要求 backup 模块权限。
		"dataDir":   dir,
		"backupDir": paths.BackupDir(),
		// 三个分类子目录一并给出：界面要按类别分组展示，光有根目录不够。
		"backupDirs": map[string]string{
			"manual": paths.ManualBackupDir(),
			"auto":   paths.AutoBackupDir(),
			"safety": paths.SafetyBackupDir(),
		},
	})
}

// handleSetDataDir 修改数据目录。
//
// 本机操作（§4.5：阶段 B/C 只在 server 本机改）。Web 版没有原生目录对话框，
// 这里接收界面输入的路径；服务端校验它确实是一个可用目录再落盘。
func (s *Server) handleSetDataDir(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperations(w, "修改数据目录") {
		return
	}
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.SetDataDir(body.Dir); err != nil {
		writeErr(w, http.StatusBadRequest, "数据目录不可用："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dataDir": s.apps.DataDir()})
}

// requireHostOperations 是破坏性 / 本机专属操作的总闸。
//
// 返回 false 时已经写过响应。403 而非 404：这些端点在阶段 B/C 确实存在、只是不
// 对该部署形态开放；用 404 会让排查的人以为端点没实现。
func (s *Server) requireHostOperations(w http.ResponseWriter, op string) bool {
	if s.hostOperations {
		return true
	}
	writeErr(w, http.StatusForbidden,
		op+"只能在服务器所在机器上执行。当前连接的是远程服务，请在服务器主机上操作。")
	return false
}

// restoreFilePattern 是「可作为恢复来源」的备份文件名规则，也是确认口令的唯一来源。
//
//	backup_20260924_170830.sql            一键备份
//	backup_20260924_170830.final.sql      同上，中间态/改名（见下方 qualifier）
//	backup_20260924_170830.upgraded.sql
//	pre_restore_20260924_172919.sql       恢复前的自动副本
//	pre_clear_20260924_105500.sql         清空前的自动副本
//
// **qualifier 是为真实数据补的**：D:\仓库数据 里躺着
// backup_20260924_170830.final.sql 与 .upgraded.sql —— 本程序自己从不产出这类名字
// （dbbackup 只写 %s_%s.sql），它们是迁移期间手工改名 / 外部工具留下的。同一时刻
// 因此会有三个世代并存。早先的规则只认结尾 .sql，于是**旧备份一个都恢复不了**，
// 而用户看到的只是一句「请选择本应用生成的文件」——那是在教用户去做软件该做的事。
//
// 限定为一个点分段、字符集 [A-Za-z0-9_-]：能覆盖 .final/.upgraded 以及以后
// .v2 之类，又不会把 `backup_20260924_170830.a b c.sql`、`..` 这类放进来。
// 结尾仍必须是 .sql / .db，所以 `xxx.sql.bak` 依旧被拒。
//
// 这条规则必须**只有一处**。它此前只活在前端 restoreTokenFor 里，于是服务端
// resolveBackupFile 只防目录穿越、不看文件名格式：界面拒绝的文件，直接调 API
// 又能恢复。两边不一致时，界面那道门就是个摆设。
var restoreFilePattern = regexp.MustCompile(
	`^(?:backup|pre_restore|pre_clear)_(\d{8}_\d{6})(?:\.[A-Za-z0-9_-]+)?\.(?:sql|db)$`)

// BackupTimestamp 从备份文件名导出时间戳，第二个返回值表示该文件是否可作为恢复来源。
//
// 前端 web/src/lib/backup.ts 的 restoreTokenFor 是它的镜像；
// internal/api/backup_contract_test.go 用同一批真实文件名锁住两者一致。
func BackupTimestamp(name string) (string, bool) {
	m := restoreFilePattern.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// resolveBackupFile 把客户端给的**文件名**解析成备份目录内的绝对路径。
//
// 拒绝任何含路径分隔符或盘符的输入，并再次确认解析结果仍在备份目录内——
// 两道检查都留是因为前者挡 `..\..\`，后者挡 symlink/junction 指向目录外。
// 这不是多余的 paranoia：客户端是 WebView2 里的网页，XSS 或手工构造请求都能到这里。
func (s *Server) resolveBackupFile(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errBackupFileRequired
	}
	if _, ok := BackupTimestamp(name); !ok {
		return "", errBackupFileNotARestore
	}
	// 跨分类目录查找（末位是旧布局根目录）。路径穿越的两道防御都在
	// backuplayout.Resolve 里：只接受文件名，且解析结果必须仍在某个备份目录内。
	target, err := backuplayout.Resolve(name)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "不能指定路径"):
			return "", errBackupFileNotInDir
		case strings.Contains(err.Error(), "不存在"):
			return "", errBackupFileMissing
		default:
			return "", errBackupFileMissing
		}
	}
	// 编码闸门：UTF-16 的 dump 按 UTF-8 读会一行 INSERT 都匹配不到，
	// 于是「全部业务表被清空、零行回填」却报告成功。service 侧还有
	// VerifyDumpFile 按字节找完成标记兜底（UTF-16 文件没有那些字节，会被拦），
	// 但那已经是在用户输完口令、点完确认之后了。这里提前拒。
	if insp, ok := s.apps.(usecase.BackupInspector); ok {
		got, err := insp.InspectBackupFile(target)
		if err != nil {
			return "", errBackupFileUnreadable
		}
		if !got.Usable {
			return "", errBackupFileEncoding(got.Reason)
		}
	}
	return target, nil
}

// ClearConfirmToken 是清空业务数据的确认口令。
//
// 服务端独立复核，不依赖界面那道门：界面提示可以被跳过，端点不能。
const ClearConfirmToken = "drop"

func urlEscape(s string) string {
	// Content-Disposition 的 filename* 用 RFC 5987；中文文件名必须这样编码，
	// 否则浏览器会按 ISO-8859-1 解出乱码。
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '-' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// restore 文件解析的四种拒绝原因。分成四条而不是一条「无效文件」：界面据此决定
// 是提示重新选择（缺失/不在目录）还是报服务端配置问题。用户看到「请选择备份文件」
// 和看到「内部错误」的处置完全不同。
var (
	errBackupFileRequired    = errors.New("请先选择要恢复的备份文件")
	errBackupFileNotInDir    = errors.New("备份文件必须位于本应用的备份目录内，不能指定路径")
	errBackupFileMissing     = errors.New("备份文件不存在或无法访问，请重新选择")
	errBackupFileNotARestore = errors.New("无法从该文件名识别备份时间戳，拒绝恢复")
	errBackupFileUnreadable  = errors.New("备份文件无法读取")
	errBackupFileEncoding    = func(reason string) error {
		if reason == "" {
			reason = "备份文件编码无法识别"
		}
		return errors.New(reason)
	}
)
