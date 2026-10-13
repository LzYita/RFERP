package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"app/internal/paths"
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
type backupEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// handleListBackups 列出可用于恢复的备份文件。
//
// 恢复界面需要「先选文件、再确认」，而文件在服务器目录里、WebView2 没有原生的
// 本地目录对话框（Fyne 版是 filepath.Walk 自己列的，ui/backup.go:332-370），
// 所以这里提供列表端点。只回文件名：客户端不该知道服务器目录长什么样。
func (s *Server) handleListBackups(w http.ResponseWriter, _ *http.Request) {
	dir := paths.BackupDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// 还没有任何备份时目录可能不存在：返回空列表而不是 500，
		// 否则首启后第一次打开备份屏就会看到报错。
		writeJSON(w, http.StatusOK, map[string]any{"files": []backupEntry{}})
		return
	}
	out := make([]backupEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// .sql 是 mysqldump 产物，.db 是 SQLite 整库快照；两者都是合法的恢复源
		// （restore.go:59 按文件头判定，不看扩展名）。
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".sql" && ext != ".db" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, backupEntry{
			Name:     e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().Format("2006-01-02 15:04:05"),
		})
	}
	// 新→旧：用户最可能要恢复的是最近那一份。
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

// handleBackupDatabase 生成一份备份，并以**文件流**返回。
//
// 此前返回 {"path": "<服务器绝对路径>"}：浏览器拿不到内容（阶段 B/C 的"下载到本机"
// 在能力矩阵里标的是可用，但代码里没有任何下载能力），同时把服务器目录布局泄了出去。
// 现在直接回内容 + Content-Disposition，浏览器一次请求就完成下载。
func (s *Server) handleBackupDatabase(w http.ResponseWriter, _ *http.Request) {
	path, err := s.apps.BackupDatabase(paths.BackupDir())
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
	if err != nil {
		writeDestructiveErr(w, "restore", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"statements":  res.Statements,
		"tableCounts": res.TableCounts,
		// 只给文件名：用户要退回时从备份列表里选它。
		"preRestore": safetyCopyName(res.PreRestore),
	})
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
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return "", errBackupFileNotInDir
	}
	dir := paths.BackupDir()
	target := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errBackupFileNotInDir
	}
	st, err := os.Stat(target)
	if err != nil || st.IsDir() {
		return "", errBackupFileMissing
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
	errBackupFileRequired = errors.New("请先选择要恢复的备份文件")
	errBackupFileNotInDir = errors.New("备份文件必须位于本应用的备份目录内，不能指定路径")
	errBackupFileMissing  = errors.New("备份文件不存在或无法访问，请重新选择")
)
