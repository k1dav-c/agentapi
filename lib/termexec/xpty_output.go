package termexec

import (
	"bufio"
	"reflect"
	"unsafe"

	"github.com/ActiveState/termtest/xpty"
)

// xptyOutput returns the bufio.Reader xpty reads the process's output
// from, so the PTY reader can take it in chunks instead of through
// xp.ReadRune (see StartProcess).
//
// The reader sits in the unexported fields Xpty.pp and
// PassthroughPipe.rdr, and nothing xpty exports will do instead.
// TerminalOutFd would have Go put the master in blocking mode (os.File.Fd
// does), so Close would no longer interrupt a read, and an os.NewFile
// around the fd would close it when collected. Reading the fields needs
// unsafe, since reflect won't hand out an unexported value. This depends
// on xpty's internals, pinned to v0.6.0; TestXptyOutputFields fails when
// an upgrade moves them.
func xptyOutput(xp *xpty.Xpty) *bufio.Reader {
	pp := unexportedField(xp, "pp").(*xpty.PassthroughPipe)
	return unexportedField(pp, "rdr").(*bufio.Reader)
}

// unexportedField returns the value of obj's unexported field name. It
// panics if there is no such field.
// Based on https://stackoverflow.com/a/60598827
func unexportedField[T any](obj *T, name string) any {
	field := reflect.ValueOf(obj).Elem().FieldByName(name)
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface()
}
