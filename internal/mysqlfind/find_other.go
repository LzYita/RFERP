//go:build !windows

package mysqlfind

type Info struct {
	ServiceName   string
	DisplayName   string
	BinDir        string
	MysqldumpPath string
	Running       bool
}

func Detect() []Info { return nil }

func ProbePort() int { return 0 }

func StartService(name string) error { return nil }
