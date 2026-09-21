package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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
	flag.Parse()

	if *zipPath == "" || *url == "" || *version == "" {
		fmt.Println("用法: signmanifest -zip 包路径 -url 下载地址 -version 1.2.3 [-notes 说明] [-min 最低版本] [-out releases.json]")
		os.Exit(2)
	}

	priv, err := loadKey(*keyFile)
	if err != nil {
		fmt.Println("读取私钥失败:", err)
		os.Exit(1)
	}

	notesText := *notes
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err != nil {
			fmt.Println("读取更新说明文件失败:", err)
			os.Exit(1)
		}
		notesText = strings.TrimRight(string(b), "\r\n \t")
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
	fmt.Println("版本:", *version)
	fmt.Println("sha256:", sum)
	fmt.Println("大小:", fi.Size())
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
