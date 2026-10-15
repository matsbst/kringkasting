package web

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"rsc.io/qr"

	"github.com/matsbst/kringkasting/internal/feedid"
)

// qrBorder is the quiet zone around a QR code, in modules (the minimum
// the QR specification allows).
const qrBorder = 4

// dialogID is the id of a feed's subscribe dialog, unique on a page:
// feed ids are unique within a result list.
func dialogID(feedID string) string {
	return "subscribe-" + notIDChars.ReplaceAllString(feedID, "-")
}

// handoffURL opens this instance's search for the feed's NRK link with the
// feed's subscribe dialog open: the address the QR code carries, so a
// podcast found on a computer is subscribed in an app on the phone.
func handoffURL(origin, feedID string) string {
	return origin + "/?query=radio.nrk.no/podkast/" + feedID + "#" + dialogID(feedID)
}

// qrCode serves the subscribe dialog's QR code as SVG. It only encodes
// handoff URLs for well-formed feed ids, never arbitrary text.
func (s *Server) qrCode(w http.ResponseWriter, r *http.Request) {
	feedID := r.URL.Query().Get("feed")
	if !feedid.Valid(feedID) {
		s.errorPage(w, r, http.StatusNotFound)
		return
	}
	code, err := qr.Encode(handoffURL(s.origin(r), feedID), qr.M)
	if err != nil {
		log.Printf("qr %s: %v", feedID, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	setCacheHeaders(w, 86400, 86400)
	_, _ = w.Write([]byte(qrSVG(code)))
}

// qrSVG draws the code as one path of horizontal runs, black on white
// whatever the page's theme, so cameras read it in dark mode too.
func qrSVG(code *qr.Code) string {
	size := strconv.Itoa(code.Size + 2*qrBorder)
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + size + ` ` + size +
		`" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="#fff"/><path fill="#000" d="`)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for code.Black(x+run, y) {
				run++
			}
			b.WriteString("M" + strconv.Itoa(x+qrBorder) + " " + strconv.Itoa(y+qrBorder) +
				"h" + strconv.Itoa(run) + "v1h-" + strconv.Itoa(run) + "z")
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
