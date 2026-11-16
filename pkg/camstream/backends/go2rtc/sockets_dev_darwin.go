//go:build darwin && camstream_dev && cgo

package go2rtc

/*
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <stdlib.h>

static int dev_owns_listener(int group, int pid, int tcp, int port, uint32_t address) {
 struct proc_bsdinfo process;
 if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &process, sizeof(process)) != sizeof(process) || process.pbi_pgid != group) return 0;
 int size = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
 if (size <= 0 || size > 1024 * 1024) return 0;
 // Allow a small number of descriptors to open between the two observations.
 size += 32 * sizeof(struct proc_fdinfo);
 struct proc_fdinfo *fds = malloc(size);
 if (!fds) return 0;
 int bytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, size);
 if (bytes <= 0) { free(fds); return 0; }
 int owned = 0;
 for (int i = 0; i < bytes / sizeof(struct proc_fdinfo); i++) {
  if (fds[i].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
  struct socket_fdinfo socket;
  if (proc_pidfdinfo(pid, fds[i].proc_fd, PROC_PIDFDSOCKETINFO, &socket, sizeof(socket)) != sizeof(socket)) continue;
  if (socket.psi.soi_family != AF_INET) continue;
  struct in_sockinfo *inet;
  if (tcp) {
   if (socket.psi.soi_kind != SOCKINFO_TCP || socket.psi.soi_proto.pri_tcp.tcpsi_state != TSI_S_LISTEN) continue;
   inet = &socket.psi.soi_proto.pri_tcp.tcpsi_ini;
  } else {
   if (socket.psi.soi_kind != SOCKINFO_IN || socket.psi.soi_protocol != IPPROTO_UDP) continue;
   inet = &socket.psi.soi_proto.pri_in;
  }
  if ((inet->insi_vflag & INI_IPV4) && ntohs(inet->insi_lport) == port && ntohl(inet->insi_laddr.ina_46.i46a_addr4.s_addr) == address) { owned = 1; break; }
 }
 free(fds);
 return owned;
}
*/
import "C"

import (
	"encoding/binary"
	"net"
)

const nativeSocketInspection = true

func nativeListenerOwned(group, pid int, check listener) bool {
	tcp := 0
	if check.network == "tcp" {
		tcp = 1
	}
	ip := net.ParseIP(check.address).To4()
	if ip == nil {
		return false
	}
	return C.dev_owns_listener(C.int(group), C.int(pid), C.int(tcp), C.int(check.port), C.uint32_t(binary.BigEndian.Uint32(ip))) == 1
}
