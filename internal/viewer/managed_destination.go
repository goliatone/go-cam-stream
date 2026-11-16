package viewer

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/goliatone/go-cam-stream/data/api"
)

type AddressLookup func(context.Context, string) ([]netip.Addr, error)

func resolveBinding(ctx context.Context, p *ManagementPolicy, lookup AddressLookup, host string, port uint16) (AddressBinding, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var addresses []netip.Addr
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		addresses = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	} else if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		if lookup == nil {
			lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
				return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			}
		}
		var err error
		addresses, err = lookup(ctx, host)
		if err != nil {
			return AddressBinding{}, api.ManagementFailure(api.CodeAddressUnavailable)
		}
	}
	if ctx.Err() != nil || len(addresses) == 0 || len(addresses) > 16 {
		return AddressBinding{}, api.ManagementFailure(api.CodeAddressUnavailable)
	}
	for j, addr := range addresses {
		addresses[j] = addr.Unmap()
		if !p.AllowsAddress(addresses[j]) {
			return AddressBinding{}, api.ManagementFailure(api.CodeDestinationDenied)
		}
	}
	slices.SortFunc(addresses, func(a, b netip.Addr) int {
		if a.Is4() != b.Is4() {
			if a.Is4() {
				return -1
			}
			return 1
		}
		return a.Compare(b)
	})
	addresses = slices.Compact(addresses)
	out := AddressBinding{Host: host, Port: port, Selected: addresses[0].String(), Addresses: []string{}}
	for _, addr := range addresses {
		out.Addresses = append(out.Addresses, addr.String())
	}
	return out, nil
}
