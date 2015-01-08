package myqlib

import (
	"os/exec"
	"os"
	"strings"
	"strconv"
	"syscall"
	"reflect"
)

// this needs some error handling and testing love
func GetTermHeight() int64 {
	cmd := exec.Command("stty", "size")
	cmd.Stdin = os.Stdin
	out, _ := cmd.Output()
	str := strings.Split( strings.TrimSpace( string( out ) ), " ")[0]
	height, _ := strconv.ParseInt( str, 10, 64)
	return height
}


func cleanupSubcmd( c *exec.Cmd ) {
	// Send the subprocess a SIGTERM when we exit
	attr := new( syscall.SysProcAttr )
	
	r := reflect.ValueOf( attr )
	f := reflect.Indirect(r).FieldByName(`Pdeathsig`)

	if f.IsValid() {
		f.Set( reflect.ValueOf( syscall.SIGTERM ) )
		c.SysProcAttr = attr
	}
}
