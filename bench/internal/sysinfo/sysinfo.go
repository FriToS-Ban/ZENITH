package sysinfo

import (
	"fmt"
	"runtime"
)

// Info holds the hardware and runtime details printed at the top of every
// benchmark report so results are reproducible and comparable across machines.
type Info struct {
	CPUs    int
	GOOS    string
	GOARCH  string
	GoVer   string
}

// Collect reads the current runtime environment.
func Collect() Info {
	return Info{
		CPUs:   runtime.NumCPU(),
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		GoVer:  runtime.Version(),
	}
}

func (i Info) String() string {
	return fmt.Sprintf("%d CPU, %s/%s, %s", i.CPUs, i.GOOS, i.GOARCH, i.GoVer)
}
