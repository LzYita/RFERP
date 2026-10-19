//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Authenticode 验签。
//
// 为什么必须有：这条链路会**下载并执行**一个来自网络的程序，而且那个安装程序
// 会请求管理员提权。整条信任链原先只有 TLS + 域名。企业内网普遍存在 TLS 拦截
// 代理，那正好把"TLS 就够安全"这个前提打掉；一旦代理或上游被投毒，我们就会以
// 用户身份（随后是管理员）执行任意代码。
//
// 边界（不夸大）：这里只验证"签名有效且链可信"，**不钉签署者**。
// 要钉「Microsoft Corporation」需要再从签名里取证书 Subject
// （WTHelperProvDataFromStateData + WTHelperGetProvSignerFromChain + 证书解析），
// 代码量与脆弱度都明显上升。当前这层能挡住"未签名/被改过的文件"，
// 挡不住"另一个有效签名的发布者"——后者需要能劫持微软的 TLS 链，门槛高得多。
//
// 离线可用：WTD_REVOKE_NONE 不查吊销列表（查的话没网就装不了），
// 代价是吊销状态不校验。这是有意的取舍，写在这里以免下次被当成疏漏。

var (
	wintrust           = syscall.NewLazyDLL("wintrust.dll")
	procWinVerifyTrust = wintrust.NewProc("WinVerifyTrust")
)

// WINTRUST_ACTION_GENERIC_VERIFY_V2
var winTrustActionGenericVerifyV2 = syscall.GUID{
	Data1: 0x00AAC56B,
	Data2: 0xCD44,
	Data3: 0x11d0,
	Data4: [8]byte{0x8c, 0xc2, 0x00, 0xc0, 0x4f, 0xc2, 0x95, 0xee},
}

const (
	wtdUINone            = 2     // WTD_UI_NONE
	wtdRevokeNone        = 0     // WTD_REVOKE_NONE
	wtdChoiceFile        = 1     // WTD_CHOICE_FILE
	wtdStateActionVerify = 1     // WTD_STATEACTION_VERIFY
	wtdStateActionClose  = 2     // WTD_STATEACTION_CLOSE
	wtdSaferFlag         = 0x100 // WTD_SAFER_FLAG
)

type winTrustFileInfo struct {
	cbStruct       uint32
	pcwszFilePath  *uint16
	hFile          syscall.Handle
	pgKnownSubject *syscall.GUID
}

type winTrustData struct {
	cbStruct            uint32
	pPolicyCallbackData uintptr
	pSIPClientData      uintptr
	dwUIChoice          uint32
	fdwRevocationChecks uint32
	dwUnionChoice       uint32
	pFile               uintptr
	dwStateAction       uint32
	hWVTStateData       syscall.Handle
	pwszURLReference    *uint16
	dwProvFlags         uint32
	dwUIContext         uint32
	pSignatureSettings  uintptr
}

// verifyAuthenticode 校验文件的 Authenticode 签名。返回 nil 表示可以执行。
func verifyAuthenticode(path string) error {
	utf16Path, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	fileInfo := winTrustFileInfo{
		cbStruct:      uint32(unsafe.Sizeof(winTrustFileInfo{})),
		pcwszFilePath: utf16Path,
	}
	data := winTrustData{
		cbStruct:            uint32(unsafe.Sizeof(winTrustData{})),
		dwUIChoice:          wtdUINone,
		fdwRevocationChecks: wtdRevokeNone,
		dwUnionChoice:       wtdChoiceFile,
		pFile:               uintptr(unsafe.Pointer(&fileInfo)),
		dwStateAction:       wtdStateActionVerify,
		dwProvFlags:         wtdSaferFlag,
	}

	action := winTrustActionGenericVerifyV2
	ret, _, _ := procWinVerifyTrust.Call(
		0,
		uintptr(unsafe.Pointer(&action)),
		uintptr(unsafe.Pointer(&data)),
	)
	// 无论成功失败都要关闭 state，否则 WinVerifyTrust 内部状态泄漏。
	data.dwStateAction = wtdStateActionClose
	procWinVerifyTrust.Call(
		0,
		uintptr(unsafe.Pointer(&action)),
		uintptr(unsafe.Pointer(&data)),
	)

	if int32(ret) != 0 {
		return fmt.Errorf("签名校验未通过（0x%08X）", uint32(ret))
	}
	return nil
}
