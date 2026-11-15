package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/geodata"
)

// What the domain rules of a geosite code cost: the matcher the router builds
// for them (it is also what holds the memory after a start) and one lookup,
// which the router makes for every new connection that has a name.
//
// Needs the real geosite.dat of the app: SLIPSTREAM_GEO_DIR is the directory
// that holds it (the app's files/geo). Without it the benchmark is skipped.
//
//	SLIPSTREAM_GEO_DIR=<dir> ./core.test -test.run xxx -test.bench AdsAll -test.benchmem
func geoDirOrSkip(tb testing.TB) string {
	tb.Helper()
	dir := os.Getenv("SLIPSTREAM_GEO_DIR")
	if dir == "" {
		tb.Skip("SLIPSTREAM_GEO_DIR is not set")
	}
	if _, err := os.Stat(filepath.Join(dir, "geosite.dat")); err != nil {
		tb.Skipf("no geosite.dat in %s", dir)
	}
	tb.Setenv("XRAY_LOCATION_ASSET", dir)
	tb.Setenv("xray.location.asset", dir)
	return dir
}

func buildGeositeMatcher(tb testing.TB, code string) geodata.DomainMatcher {
	tb.Helper()
	rules, err := geodata.ParseDomainRules([]string{"geosite:" + code}, geodata.Domain_Domain)
	if err != nil {
		tb.Fatalf("ParseDomainRules(%s): %v", code, err)
	}
	m, err := geodata.DomainReg.BuildDomainMatcher(rules)
	if err != nil {
		tb.Fatalf("BuildDomainMatcher(%s): %v", code, err)
	}
	return m
}

func BenchmarkAdsAllMatcherBuild(b *testing.B) {
	geoDirOrSkip(b)
	for _, code := range []string{"category-ru", "category-ads-all"} {
		b.Run(code, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				buildGeositeMatcher(b, code)
			}
		})
	}
}

func BenchmarkAdsAllMatcherLookup(b *testing.B) {
	geoDirOrSkip(b)
	m := buildGeositeMatcher(b, "category-ads-all")
	// A name the rules know, one they do not, and a long one under a known domain.
	names := map[string]string{
		"hit":           "doubleclick.net",
		"miss":          "example.com",
		"miss_long":     "a.very.long.subdomain.of.some.ordinary.website.example.org",
		"hit_subdomain": "x.y.ads.doubleclick.net",
	}
	for label, name := range names {
		b.Run(label, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m.MatchAny(name)
			}
		})
	}
}

// The memory that stays with the matcher once it is built, in MB, and the
// time it took: the same figures the start of a connection pays once.
func TestAdsAllMatcherSize(t *testing.T) {
	geoDirOrSkip(t)
	for _, code := range []string{"category-ru", "category-ads-all"} {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		start := time.Now()
		m := buildGeositeMatcher(t, code)
		took := time.Since(start)
		runtime.GC()
		runtime.ReadMemStats(&after)
		t.Logf("%s: built in %v, holds %.1f MB", code, took, float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/(1<<20))
		runtime.KeepAlive(m)
	}
}
