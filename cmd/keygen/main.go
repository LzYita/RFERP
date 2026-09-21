package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Println("生成密钥失败:", err)
		os.Exit(1)
	}
	privB64 := base64.StdEncoding.EncodeToString(priv)
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	const privFile = "rferp-update-private.key"
	const pubFile = "rferp-update-public.key"
	if err := os.WriteFile(privFile, []byte(privB64+"\n"), 0600); err != nil {
		fmt.Println("写入私钥失败:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(pubFile, []byte(pubB64+"\n"), 0644); err != nil {
		fmt.Println("写入公钥失败:", err)
		os.Exit(1)
	}

	fmt.Println("已生成更新签名密钥对：")
	fmt.Println("  私钥（务必保密，不要提交/外发）:", privFile)
	fmt.Println("  公钥:", pubFile)
	fmt.Println()
	fmt.Println("请把下面这行公钥交给开发者，编进程序：")
	fmt.Println(pubB64)
}
