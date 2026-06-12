package crawler

import (
	"net"
	"time"

	"github.com/decred/dcrd/wire"
)

type Node struct {
	Services        wire.ServiceFlag
	LastAttempt     time.Time
	LastSuccess     time.Time
	ProtocolVersion uint32
	IP              net.IP
	UserAgent       string
	GeoData         *GeoData

	// good tracks whether the node is currently in the good set. It mirrors
	// membership of Manager.goodNodes so that membership tests are O(1) rather
	// than requiring a linear scan. Being unexported it is never serialized; it
	// is recomputed from LastSuccess on startup.
	good bool
}

type GeoData struct {
	City      string
	Region    string
	Country   string
	Continent string
	Org       string
	ISP       string
	AS        string
	ASName    string
	Lat       float32
	Lon       float32
}
