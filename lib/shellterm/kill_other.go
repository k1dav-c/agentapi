//go:build !unix

package shellterm

import "os"

func killGroup(p *os.Process) { _ = p.Kill() }
