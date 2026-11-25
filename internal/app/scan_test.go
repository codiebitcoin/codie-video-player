package app

import (
	"net"
	"testing"
)

func TestHostsOfSkipsNetworkAndBroadcast(t *testing.T) {
	// A scan walks the usable addresses of a network: not the network's own,
	// and not its broadcast.
	_, n, err := net.ParseCIDR("10.0.0.0/30")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for ip := range hostsOf(*n) {
		got = append(got, ip)
	}
	want := []string{"10.0.0.1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("hosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hosts = %v, want %v", got, want)
		}
	}
}
