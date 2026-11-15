//go:build linux

package app

import (
	"strings"
	"testing"
	"testing/fstest"
)

const validSystemd = "MainPID=42\nControlGroup=/system.slice/camstream.service\nKillMode=control-group\nSendSIGKILL=yes\nRestart=on-failure\nDelegate=no\nTimeoutStopUSec=1min\nUser=camstream\nType=exec\nExitType=main\nActiveState=active\n"

func TestSystemdBoundaryPolicy(t *testing.T) {
	if !systemdProperties(validSystemd, 42) {
		t.Fatal("valid properties")
	}
	for _, pair := range [][2]string{{"MainPID=42", "MainPID=43"}, {"KillMode=control-group", "KillMode=process"}, {"Delegate=no", "Delegate=yes"}, {"SendSIGKILL=yes", "SendSIGKILL=no"}, {"Restart=on-failure", "Restart=no"}, {"TimeoutStopUSec=1min", "TimeoutStopUSec=infinity"}, {"User=camstream", "User=root"}, {"Type=exec", "Type=forking"}, {"ExitType=main", "ExitType=cgroup"}, {"ActiveState=active", "ActiveState=inactive"}, {"/system.slice/camstream.service", "/user.slice/camstream.service"}} {
		if systemdProperties(strings.Replace(validSystemd, pair[0], pair[1], 1), 42) {
			t.Fatal("accepted", pair)
		}
	}
	if systemdProperties(validSystemd+"MainPID=42\n", 42) {
		t.Fatal("duplicate property")
	}
}
func TestSystemdOldDescendantsBlockAdmission(t *testing.T) {
	for _, test := range []struct {
		root, child string
		ok          bool
	}{{"42\n", "", true}, {"42\n99\n", "", false}, {"42\n", "99\n", false}, {"", "", false}, {"oops", "", false}} {
		tree := fstest.MapFS{"cgroup.procs": {Data: []byte(test.root)}, "child/cgroup.procs": {Data: []byte(test.child)}}
		if got := cleanCgroup(tree, 42) == nil; got != test.ok {
			t.Fatalf("%+v: got %v", test, got)
		}
	}
}
