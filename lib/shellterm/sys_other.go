//go:build !unix

package shellterm

import "os"

func killGroup(p *os.Process) { _ = p.Kill() }

func nonblocking(ptmx *os.File) *os.File { return ptmx }
