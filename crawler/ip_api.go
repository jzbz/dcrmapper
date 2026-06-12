package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"time"
)

type ipAPIResponse struct {
	Status    string  `json:"status"`
	City      string  `json:"city"`
	Region    string  `json:"regionName"`
	Country   string  `json:"country"`
	Continent string  `json:"continent"`
	ISP       string  `json:"isp"`
	Org       string  `json:"org"`
	AS        string  `json:"as"`
	ASName    string  `json:"asname"`
	Lat       float32 `json:"lat"`
	Lon       float32 `json:"lon"`
	Query     string  `json:"query"`
}

const (
	// Plain HTTP because the ip-api free tier does not support HTTPS. The
	// data is public geolocation info, not secrets.
	ipapiurl = "http://ip-api.com/batch?fields=status,city,regionName,country,continent,isp,org,as,asname,lat,lon,query"

	// ipapiBatchSize is the maximum number of IPs accepted per ip-api batch
	// request.
	ipapiBatchSize = 100
)

func (m *Manager) geoIP(ctx context.Context) {
	// Skip the cycle entirely when shutdown has already been requested.
	if ctx.Err() != nil {
		return
	}

	client := http.Client{
		Timeout: time.Second * 10,
	}

	m.mtx.RLock()
	toFind := make([]string, 0, len(m.nodes))
	for ip, node := range m.nodes {
		if node.GeoData == nil {
			toFind = append(toFind, ip)
		}
	}
	m.mtx.RUnlock()

	if len(toFind) == 0 {
		return
	}

	log.Printf("Missing geo data for %d nodes", len(toFind))

	// Process IPs in batches of at most ipapiBatchSize, the max supported by
	// ip-api. lookupBatch reports whether crawling should continue.
	for batch := range slices.Chunk(toFind, ipapiBatchSize) {
		if !m.lookupBatch(ctx, &client, batch) {
			return
		}
	}
}

// lookupBatch geolocates a single batch of IPs and applies the results. It
// returns false when the caller should stop (context cancelled or an
// unrecoverable error).
func (m *Manager) lookupBatch(ctx context.Context, client *http.Client, batch []string) bool {
	reqData, err := json.Marshal(batch)
	if err != nil {
		log.Printf("json.Marshal error: %v", err)
		return false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ipapiurl, bytes.NewReader(reqData))
	if err != nil {
		log.Printf("http.NewRequestWithContext error: %v", err)
		return false
	}

	res, err := client.Do(req)
	if err != nil {
		log.Printf("client.Do error: %v", err)
		return false
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("io.ReadAll: %v", err)
		return false
	}

	var geos []ipAPIResponse
	if err := json.Unmarshal(body, &geos); err != nil {
		if res.StatusCode == http.StatusTooManyRequests {
			log.Printf("Unexpectedly hit ip-api rate limit, waiting 60 seconds")
			return sleep(ctx, 60*time.Second)
		}

		log.Printf("json.Unmarshal error: %v (status %s): %s", err, res.Status, body)
		return false
	}

	log.Printf("Got %d geo locations", len(geos))

	m.mtx.Lock()
	for _, geo := range geos {
		if geo.Status != "success" {
			log.Printf("Skipping non-success geo, status: %s, query: %s", geo.Status, geo.Query)
			continue
		}

		node, ok := m.nodes[geo.Query]
		if !ok {
			log.Printf("Received geo for non-existing node %s", geo.Query)
			continue
		}
		node.GeoData = &GeoData{
			City:      geo.City,
			Region:    geo.Region,
			Country:   geo.Country,
			Continent: geo.Continent,
			ISP:       geo.ISP,
			Org:       geo.Org,
			AS:        geo.AS,
			ASName:    geo.ASName,
			Lat:       geo.Lat,
			Lon:       geo.Lon,
		}
	}
	m.mtx.Unlock()

	// Respect the rate limit advertised in the response headers.
	remainingReqs, err := strconv.Atoi(res.Header.Get("X-Rl"))
	if err != nil {
		log.Printf("ip-api response missing X-Rl header")
		return false
	}
	if remainingReqs == 0 {
		timeToWait, err := strconv.Atoi(res.Header.Get("X-Ttl"))
		if err != nil {
			log.Printf("ip-api response missing X-Ttl header")
			return false
		}

		log.Printf("Hit ip-api rate limit. Waiting %d seconds", timeToWait+5)
		return sleep(ctx, time.Duration(timeToWait+5)*time.Second)
	}

	return true
}

// sleep blocks for d or until ctx is cancelled. It returns false if ctx was
// cancelled, signalling the caller to stop.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
