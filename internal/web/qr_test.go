package web

import (
	"fmt"
	"strings"
	"testing"

	"rsc.io/qr"
)

func TestHandoffURL(t *testing.T) {
	for feedID, want := range map[string]string{
		"sentralbordet": "https://kringkast.ing/?query=radio.nrk.no/podkast/sentralbordet#subscribe-sentralbordet",
		"musikkhistorier/sesong/miles-davis--bak-mytene": "https://kringkast.ing/?query=radio.nrk.no/podkast/" +
			"musikkhistorier/sesong/miles-davis--bak-mytene#subscribe-musikkhistorier-sesong-miles-davis--bak-mytene",
	} {
		if got := handoffURL("https://kringkast.ing", feedID); got != want {
			t.Errorf("%s: %s", feedID, got)
		}
	}
}

// The SVG must draw exactly the code's modules, inside the quiet zone.
func TestQRSVGMatchesCode(t *testing.T) {
	code, err := qr.Encode(handoffURL("https://kringkast.ing", "musikkhistorier/sesong/miles-davis--bak-mytene"), qr.M)
	if err != nil {
		t.Fatal(err)
	}
	svg := qrSVG(code)
	if !strings.Contains(svg, fmt.Sprintf(`viewBox="0 0 %d %d"`, code.Size+8, code.Size+8)) {
		t.Fatalf("viewBox lacks the quiet zone: %.120s", svg)
	}
	path := svg[strings.Index(svg, ` d="`)+4:]
	path = path[:strings.Index(path, `"`)]
	drawn := map[[2]int]bool{}
	for _, segment := range strings.Split(strings.TrimSuffix(path, "z"), "z") {
		var x, y, run, back int
		if _, err := fmt.Sscanf(segment, "M%d %dh%dv1h-%d", &x, &y, &run, &back); err != nil || run != back {
			t.Fatalf("segment %q: %v", segment, err)
		}
		for i := range run {
			drawn[[2]int{x + i - 4, y - 4}] = true
		}
	}
	for y := -4; y < code.Size+4; y++ {
		for x := -4; x < code.Size+4; x++ {
			if drawn[[2]int{x, y}] != code.Black(x, y) {
				t.Fatalf("module %d,%d drawn=%v", x, y, drawn[[2]int{x, y}])
			}
		}
	}
}
