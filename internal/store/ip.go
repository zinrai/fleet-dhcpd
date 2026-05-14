package store

import (
	"encoding/binary"
	"net"
)

// ipToUint32 converts a 4-byte IPv4 address to uint32.
func ipToUint32(ip net.IP) uint32 {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(v4)
}

// uint32ToIP converts a uint32 to a 4-byte IPv4 address.
func uint32ToIP(n uint32) net.IP {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return net.IP(b)
}
