package usecase

import "fmt"

// APIVersion 是当前程序能说 / 服务端能听的 HTTP 接口版本。
// 两边不一致时 PlanLink 判定为 LinkIncompatible，避免用旧客户端连新服务端。
const APIVersion = 1

// LinkKind 是「本机 → 服务器」接入的判定结果类别。
type LinkKind int

const (
	// LinkSameDatabase：目标服务器使用的就是本机同一个数据库。
	// 切换运行模式不会改变任何数据，不需要迁移。
	LinkSameDatabase LinkKind = iota
	// LinkOtherDatabase：目标服务器使用另一个数据库。
	// 必须先迁移数据，否则等于换了一套账。
	LinkOtherDatabase
	// LinkIncompatible：接口版本不兼容，先升级再接入。
	LinkIncompatible
)

func (k LinkKind) String() string {
	switch k {
	case LinkSameDatabase:
		return "same-database"
	case LinkOtherDatabase:
		return "other-database"
	case LinkIncompatible:
		return "incompatible"
	}
	return "unknown"
}

// LinkPlan 是接入判定的完整结论，供 UI 展示与后续动作决策。
type LinkPlan struct {
	Kind          LinkKind
	Reason        string
	NeedsDataMove bool
}

// PlanLink 比较本机与目标服务器的事实，给出接入结论。
//
// 纯函数：不碰网络、不碰数据库，便于单测。
// 判定顺序：接口版本兼容 → 数据库身份是否相同 → 结论。
func PlanLink(local, remote ServerInfo) LinkPlan {
	if local.APIVersion != remote.APIVersion {
		return LinkPlan{
			Kind: LinkIncompatible,
			Reason: fmt.Sprintf("接口版本不匹配：本机 %d，服务器 %d，请先升级到一致版本",
				local.APIVersion, remote.APIVersion),
		}
	}
	if local.DatabaseID == "" || remote.DatabaseID == "" {
		// 拿不到身份就不能假设是同一个库，按最保守处理。
		return LinkPlan{
			Kind:          LinkOtherDatabase,
			Reason:        "无法确认数据库身份（缺少数据库标识），按不同数据库处理",
			NeedsDataMove: true,
		}
	}
	if local.DatabaseID == remote.DatabaseID {
		return LinkPlan{
			Kind:   LinkSameDatabase,
			Reason: "服务器使用的正是本机同一个数据库，切换后数据完全一致，无需迁移",
		}
	}
	return LinkPlan{
		Kind:          LinkOtherDatabase,
		Reason:        "服务器使用的是另一个数据库，切换前需要先迁移数据",
		NeedsDataMove: true,
	}
}
