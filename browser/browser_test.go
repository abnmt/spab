package browser

import "testing"

func TestLocalURL(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0:8090":    "http://127.0.0.1:8090",
		":8090":           "http://127.0.0.1:8090",
		"[::]:8800":       "http://127.0.0.1:8800",
		"127.0.0.1:8800":  "http://127.0.0.1:8800",
		" 0.0.0.0:9999 ":  "http://127.0.0.1:9999",
		"192.168.1.10:80": "http://192.168.1.10:80",
		"nas.local:8090":  "http://nas.local:8090",
		"[::1]:8090":      "http://[::1]:8090",
		"без-порта":       "http://без-порта",
	}
	for addr, want := range cases {
		if got := LocalURL(addr); got != want {
			t.Errorf("LocalURL(%q) = %q, ждали %q", addr, got, want)
		}
	}
}
