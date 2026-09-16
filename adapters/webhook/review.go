// The review queue sink: one HTTP POST per flagged line to a Power Automate
// trigger.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dramanclancy/poalib/domain"
)

// Sink posts flagged lines to a Power Automate "When an HTTP request is
// received" trigger. A zero URL means the queue is not configured.
type Sink struct {
	URL    string
	Client *http.Client
	Log    func(format string, args ...any)
}

// New returns a sink, or nil when no URL is configured — a nil ReviewSink is
// the disabled state and the pipeline skips it.
func New(url string, log func(string, ...any)) *Sink {
	if url == "" {
		return nil
	}
	return &Sink{URL: url, Client: &http.Client{Timeout: 10 * time.Second}, Log: log}
}

// line is one flagged row on the wire. The field names mirror
// domain.ReviewLine directly — Power Automate reads them as-is, with no
// reshaping on either side, so renaming one orphans whatever already matches
// against it.
type line struct {
	PF               string        `json:"pf"`
	VendorNo         string        `json:"vendorNo"`
	VendorName       string        `json:"vendorName"`
	BCLineID         string        `json:"bcLineId"`
	BCItemNo         string        `json:"bcItemNo"`
	POALineIndex     int           `json:"poaLineIndex"`
	EngineVersion    string        `json:"engineVersion"`
	RowHash          string        `json:"rowHash"`
	POADescription   string        `json:"poaDescription"`
	BCDescription    string        `json:"bcDescription"`
	DescScore        float64       `json:"descScore"`
	DescMargin       float64       `json:"descMargin"`
	CodeMatchSource  string        `json:"codeMatchSource"`
	CodeDataState    string        `json:"codeDataState"`
	SeatDataState    string        `json:"seatDataState"`
	POACodeCandidate string        `json:"poaCodeCandidate"`
	POAQty           float64       `json:"poaQty"`
	BCQty            float64       `json:"bcQty"`
	POANet           float64       `json:"poaNet"`
	BCNet            float64       `json:"bcNet"`
	Discrepancies    []discrepancy `json:"discrepancies"`
}

// discrepancy adds the wire-stable learnability string to each reason. The
// flow's "may this verdict be recorded as correct" condition must read THIS
// field rather than re-listing kinds itself — the domain stays the one place
// that answer lives.
type discrepancy struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	Learnability string `json:"learnability"` // "never" / "per_line" / "per_vendor"
}

func toWire(rl domain.ReviewLine) line {
	ds := make([]discrepancy, 0, len(rl.Discrepancies))
	for _, d := range rl.Discrepancies {
		ds = append(ds, discrepancy{
			Kind:         string(d.Kind),
			Message:      d.Message,
			Learnability: d.Kind.Learnability().String(),
		})
	}
	return line{
		PF: rl.PF, VendorNo: rl.VendorNo, VendorName: rl.VendorName,
		BCLineID: rl.BCLineID, BCItemNo: rl.BCItemNo, POALineIndex: rl.POALineIndex,
		EngineVersion: rl.EngineVersion, RowHash: rl.RowHash,
		POADescription: rl.POADescription, BCDescription: rl.BCDescription,
		DescScore: rl.DescScore, DescMargin: rl.DescMargin,
		CodeMatchSource: rl.CodeMatchSource, CodeDataState: rl.CodeDataState,
		SeatDataState: rl.SeatDataState, POACodeCandidate: rl.POACodeCandidate,
		POAQty: rl.POAQty, BCQty: rl.BCQty, POANet: rl.POANet, BCNet: rl.BCNet,
		Discrepancies: ds,
	}
}

// Publish sends one POST per flagged line, so the flow can create rows and
// post cards without waiting for the rest of the order.
//
// Runs synchronously, before the response is written: a goroutine started here
// has no guarantee of finishing on a Consumption-plan Function once the
// response is sent. Individual failures are logged and swallowed — the
// workbook is still valid and still returned.
func (s *Sink) Publish(ctx context.Context, pf string, lines []domain.ReviewLine) error {
	if s == nil || s.URL == "" || len(lines) == 0 {
		return nil
	}
	var failed int
	for _, rl := range lines {
		if err := s.post(ctx, toWire(rl)); err != nil {
			failed++
			s.logf("posting review line for %s line %d: %s", pf, rl.POALineIndex, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d review lines failed to post", failed, len(lines))
	}
	return nil
}

func (s *Sink) post(ctx context.Context, l line) error {
	body, err := json.Marshal(l)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *Sink) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
		return
	}
	log.Printf(format, args...)
}
