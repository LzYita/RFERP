package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"app/internal/update"
)

func main() {
	keyFile := flag.String("key", "rferp-update-private.key", "Ed25519 私钥文件")
	zipPath := flag.String("zip", "", "更新包 zip 路径")
	url := flag.String("url", "", "更新包下载地址")
	version := flag.String("version", "", "版本号，如 1.2.3")
	notes := flag.String("notes", "", "更新说明")
	notesFile := flag.String("notes-file", "", "更新说明文件（优先于 -notes）")
	minVer := flag.String("min", "", "最低可升级版本（可选）")
	channel := flag.String("channel", "stable", "更新通道")
	out := flag.String("out", "releases.json", "输出清单文件")
	changelog := flag.String("changelog", "", "内置更新说明文件（internal/help/changelog.json）；给定则把本次版本累积进去")
	changelogOnly := flag.String("changelog-only", "", "只把本次版本累积进该内置更新说明文件然后退出，不需要私钥与更新包。必须在构建之前调用：更新说明靠 go:embed 打进二进制，构建之后再累积就来不及了")
	flag.Parse()

	if *changelogOnly != "" {
		if err := accumulate(*changelogOnly, *version, *notes, *notesFile); err != nil {
			fmt.Println("累积内置更新说明失败:", err)
			os.Exit(1)
		}
		fmt.Println("已累积内置更新说明（构建前）:", *changelogOnly)
		return
	}

	if *zipPath == "" || *url == "" || *version == "" {
		fmt.Println("用法: signmanifest -zip 包路径 -url 下载地址 -version 1.2.3 [-notes 说明] [-min 最低版本] [-out releases.json]")
		os.Exit(2)
	}

	priv, err := loadKey(*keyFile)
	if err != nil {
		fmt.Println("读取私钥失败:", err)
		os.Exit(1)
	}

	notesText, err := readNotes(*notes, *notesFile)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fi, err := os.Stat(*zipPath)
	if err != nil {
		fmt.Println("更新包不存在:", err)
		os.Exit(1)
	}
	f, err := os.Open(*zipPath)
	if err != nil {
		fmt.Println("打开更新包失败:", err)
		os.Exit(1)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		fmt.Println("计算哈希失败:", err)
		os.Exit(1)
	}
	f.Close()
	sum := hex.EncodeToString(h.Sum(nil))

	m := update.Manifest{
		Version:     *version,
		Channel:     *channel,
		MinVersion:  *minVer,
		URL:         *url,
		Size:        fi.Size(),
		SHA256:      sum,
		Notes:       notesText,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := m.Sign(priv); err != nil {
		fmt.Println("签名失败:", err)
		os.Exit(1)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fmt.Println("序列化失败:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0644); err != nil {
		fmt.Println("写入清单失败:", err)
		os.Exit(1)
	}
	fmt.Println("已生成并签名清单:", *out)

	// 把本次版本累积进内置更新说明（设置页的「更新说明」读取该文件）。
	// 同版本重跑时替换该条，避免重复累积。
	if *changelog != "" {
		if err := appendChangelog(*changelog, m.Version, m.PublishedAt, m.Notes); err != nil {
			// 不让累积失败阻断发布：清单已经签好署好名，更新功能本身可用。
			fmt.Println("警告: 累积内置更新说明失败（不影响本次发布）:", err)
		} else {
			fmt.Println("已累积内置更新说明:", *changelog)
		}
	}

	fmt.Println("版本:", *version)
	fmt.Println("sha256:", sum)
	fmt.Println("大小:", fi.Size())
}

// changelogFile 只声明我们关心的字段。
type changelogFile struct {
	Versions []changelogEntry `json:"versions"`
}

type changelogEntry struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Notes       string `json:"notes"`
}

// accumulate 把一个版本写进内置更新说明文件，不需要私钥与更新包。
//
// release.bat 在构建 exe 之前调用它：更新说明通过 go:embed 编译进二进制，
// 若在构建之后才累积，本次发布的程序里就不会有本次的更新说明
// （「更新说明」页与指南顶部的「内置记录最新」都会停在上一版）。
func accumulate(path, version, notes, notesFile string) error {
	if path == "" {
		return errors.New("未指定更新说明文件")
	}
	if version == "" {
		return errors.New("未指定版本号")
	}
	text, err := readNotes(notes, notesFile)
	if err != nil {
		return err
	}
	return appendChangelog(path, version, time.Now().UTC().Format(time.RFC3339), text)
}

// readNotes 读取更新说明正文：-notes-file 优先于 -notes。
func readNotes(notes, notesFile string) (string, error) {
	if notesFile == "" {
		return strings.TrimRight(notes, "\r\n \t"), nil
	}
	b, err := os.ReadFile(notesFile)
	if err != nil {
		return "", fmt.Errorf("读取更新说明文件 %s 失败: %w", notesFile, err)
	}
	return strings.TrimRight(string(b), "\r\n \t"), nil
}

// appendChangelog 把一个版本写入更新说明文件，按版本号从新到旧排序。
//
// 文件不存在时创建；同版本已存在则替换其内容（重跑发布不会产生重复条目）。
//
// 只改写 versions 字段：文件里的其它键（如给维护者看的 $comment）用
// json.RawMessage 原样带回，避免累积过程静默丢掉它们。
func appendChangelog(path, version, publishedAt, notes string) error {
	// 根对象保留为原始键值对，未知字段不动
	root := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &root); err != nil {
			return fmt.Errorf("已有更新说明文件无法解析（请手工修复后重试）: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	var versions []changelogEntry
	if raw, ok := root["versions"]; ok {
		if err := json.Unmarshal(raw, &versions); err != nil {
			return fmt.Errorf("已有 versions 字段无法解析（请手工修复后重试）: %w", err)
		}
	}

	entry := changelogEntry{Version: version, PublishedAt: publishedAt, Notes: notes}
	replaced := false
	out := make([]changelogEntry, 0, len(versions)+1)
	for _, e := range versions {
		if e.Version == version {
			out = append(out, entry)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return update.CompareVersions(out[i].Version, out[j].Version) > 0
	})

	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	root["versions"] = raw

	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	// 末尾补换行，保持文件在 Git 里的可读性
	b = append(b, '\n')
	return os.WriteFile(path, b, 0644)
}

func loadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("私钥长度无效")
	}
	return ed25519.PrivateKey(raw), nil
}
