// Package netinfo 汇总本机的局域网 IPv4，供服务端启动日志与安装器展示。
//
// 为什么单独成包：多网卡/多 IP 是局域网版最常见的连不上原因（R29）——
// 主机上既有有线网卡、WiFi，也可能有 VMware/Hyper-V 虚拟网卡和 Docker 网桥。
// 只要启动日志把"所有可用地址"都列出来，客户就能自己判断该填哪个，
// 不用远程排障（本版没有远程支持，D-24）。
package netinfo

import (
	"net"
	"sort"
)

// Candidate 是一条可在客户端手填的访问地址。
type Candidate struct {
	Interface string // 网卡名，例如「以太网」
	IP        string
	// Private 标记是否属于 RFC1918 私有地址段。私有段优先展示：
	// 它们才最可能是"同一局域网"里的那个地址。
	Private bool
}

// LANIPv4s 返回所有已启用、非回环网卡上的 IPv4 地址。
//
// 只做事实枚举，不猜"主 IP"：主 IP 取决于路由表，服务端此刻选错了也不会
// 影响运行，只会误导客户。按「私有段优先 + 地址升序」排序，让文档里的
// 192.168.x.x 稳定出现在第一行。
func LANIPv4s() []Candidate {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Candidate
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, Candidate{
				Interface: iface.Name,
				IP:        ip4.String(),
				Private:   ip4.IsPrivate(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Private != out[j].Private {
			return out[i].Private
		}
		return out[i].IP < out[j].IP
	})
	return out
}

// AccessURLs 把候选地址拼成客户端可直接填写的 URL。
//
// port 允许带冒号前缀（":8080"）或纯端口（"8080"），两种都接受：
// 服务端监听地址与安装器参数来源不同，统一在这一处归一。
func AccessURLs(port string) []string {
	if port == "" {
		return nil
	}
	if port[0] != ':' {
		port = ":" + port
	}
	cands := LANIPv4s()
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, "http://"+c.IP+port)
	}
	return out
}
