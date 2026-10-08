package api

import "testing"

func TestTierFeaturesFillTheDefaultWindowAndEchoWhatIsStored(t *testing.T) {
	cases := map[string]struct {
		window, stored int
	}{
		"given":    {window: 3600, stored: 3600},
		"omitted":  {window: 0, stored: defaultWindowSeconds},
		"negative": {window: -5, stored: defaultWindowSeconds},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			stored, echoed := tierFeatures("tier-1", map[string]featureLimitBody{
				"chat": {RateLimit: 30, WindowSeconds: c.window},
			})
			if len(stored) != 1 || stored[0].TierID != "tier-1" || stored[0].RateLimit != 30 || stored[0].WindowSeconds != c.stored {
				t.Fatalf("stored %+v", stored[0])
			}
			want := featureLimitBody{RateLimit: 30, WindowSeconds: c.stored}
			if echoed["chat"] != want {
				t.Fatalf("echoed %+v, want %+v", echoed["chat"], want)
			}
		})
	}
}
