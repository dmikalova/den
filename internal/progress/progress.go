// Package progress prints a single-line transfer status that refreshes in
// place, so a long download or write shows it is still moving.
package progress

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Reporter redraws one line at most once a second.
type Reporter struct {
	w     io.Writer
	label string
	start time.Time
	last  time.Time
	width int
	done  int64
	total int64
}

// New makes a Reporter that writes to w. The label starts every line.
func New(w io.Writer, label string) *Reporter {
	now := time.Now()
	return &Reporter{w: w, label: label, start: now, last: now}
}

// Update records progress and redraws if a second has passed. A total of 0 or
// less means the size is unknown, which is what a server that sends no
// Content-Length gives us.
func (r *Reporter) Update(done, total int64) {
	r.done, r.total = done, total
	if time.Since(r.last) < time.Second {
		return
	}
	r.last = time.Now()
	r.draw()
}

// Done redraws the final numbers and ends the line.
func (r *Reporter) Done() {
	r.draw()
	_, _ = fmt.Fprintln(r.w)
}

func (r *Reporter) draw() {
	elapsed := time.Since(r.start).Seconds()
	var rate string
	if elapsed > 0 {
		rate = fmt.Sprintf("  %s/s", humanBytes(int64(float64(r.done)/elapsed)))
	}

	line := fmt.Sprintf("%s  %s", r.label, humanBytes(r.done))
	if r.total > 0 {
		line = fmt.Sprintf("%s  %s / %s  %3.0f%%",
			r.label, humanBytes(r.done), humanBytes(r.total),
			float64(r.done)/float64(r.total)*100)
	}
	line += rate

	// Pad over whatever the previous, possibly longer, line left behind.
	pad := ""
	if n := r.width - len(line); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	r.width = len(line)
	_, _ = fmt.Fprintf(r.w, "\r%s%s", line, pad)
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
