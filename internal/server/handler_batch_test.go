package server

import (
	"strings"
	"testing"
	"time"
)

func TestBatch_NamesWrappedInBatch(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	// alice negotiates batch.
	cAlice, rAlice := dialClient(t, addr)
	defer cAlice.Close()
	cAlice.Write([]byte("CAP LS\r\n"))
	cAlice.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatalf("waiting for CAP LS: %v", err)
		}
		if strings.Contains(line, " CAP ") && strings.Contains(line, " LS ") {
			break
		}
	}
	cAlice.Write([]byte("CAP REQ :batch\r\n"))
	cAlice.Write([]byte("NICK alice\r\nUSER alice 0 * :Alice\r\n"))
	cAlice.Write([]byte("CAP END\r\n"))
	expectNumeric(t, cAlice, rAlice, "001", time.Now().Add(2*time.Second))

	// JOIN triggers a NAMES burst which should be batched.
	cAlice.Write([]byte("JOIN #batched\r\n"))

	dl := time.Now().Add(2 * time.Second)
	cAlice.SetReadDeadline(dl)

	// Collect lines until we see BATCH -ref (the batch end marker).
	// Lines may carry @tags so extractNumeric may fail; we match
	// on substrings instead.
	var lines []string
	for {
		line, err := rAlice.ReadString('\n')
		if err != nil {
			t.Fatalf("reading JOIN/NAMES burst: %v (got %d lines)\nlines: %v", err, len(lines), lines)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		lines = append(lines, trimmed)
		// The BATCH -ref may be encoded as a trailing param with ":"
		if strings.Contains(trimmed, "BATCH -") || strings.Contains(trimmed, "BATCH :-") {
			break
		}
	}

	// Find BATCH +ref and BATCH -ref. The ref may be a middle or
	// trailing param (with leading ":"), so we strip both.
	batchStart := ""
	batchEnd := ""
	for _, l := range lines {
		if strings.Contains(l, "BATCH") {
			parts := strings.Fields(l)
			for _, p := range parts {
				p = strings.TrimPrefix(p, ":")
				if strings.HasPrefix(p, "+ircat") {
					batchStart = p[1:]
				}
				if strings.HasPrefix(p, "-ircat") {
					batchEnd = p[1:]
				}
			}
		}
	}

	if batchStart == "" {
		t.Fatalf("missing BATCH +ref in NAMES burst\nlines: %v", lines)
	}
	if batchEnd == "" {
		t.Fatalf("missing BATCH -ref after NAMES burst\nlines: %v", lines)
	}
	if batchStart != batchEnd {
		t.Errorf("batch ref mismatch: start=%q end=%q", batchStart, batchEnd)
	}

	// 353 lines should carry @batch=ref tag. Lines may have @tags
	// before the prefix so we match on " 353 " substring.
	for _, l := range lines {
		if strings.Contains(l, " 353 ") {
			if !strings.Contains(l, "batch="+batchStart) {
				t.Errorf("353 missing @batch tag: %q", l)
			}
		}
	}
}

func TestBatch_NamesWithoutCap_NoBatch(t *testing.T) {
	addr, teardown := startTestServer(t)
	defer teardown()

	c, r := register(t, addr, "alice")
	defer c.Close()

	c.Write([]byte("JOIN #nobatch\r\n"))

	dl := time.Now().Add(2 * time.Second)
	c.SetReadDeadline(dl)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.Contains(trimmed, "BATCH") {
			t.Errorf("client without batch cap should not see BATCH: %q", trimmed)
		}
		if extractNumeric(trimmed) == "366" {
			break
		}
	}
}
