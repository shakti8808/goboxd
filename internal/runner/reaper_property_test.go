package runner_test

import (
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// TestPropertyReapOnceOnlyJobPrefix — Property 40 surface.
//
// For arbitrary directory names placed under the sandbox root with
// arbitrary mtimes, ReapOnce removes a directory iff:
//
//   - the basename starts with "job-", AND
//   - (now - mtime) > TTL.
//
// Anything else is left untouched. The property runs ≥100 rapid
// iterations per invocation per Req I-4.2.
func TestPropertyReapOnceOnlyJobPrefix(outer *testing.T) {
	rapid.Check(outer, func(t *rapid.T) {
		const root = "/sandbox"
		ttl := 10 * time.Minute
		now := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)

		mfs := newMemFS()
		entryCount := rapid.IntRange(0, 8).Draw(t, "entryCount")
		want := map[string]bool{} // path -> should be removed

		for i := 0; i < entryCount; i++ {
			isJob := rapid.Bool().Draw(t, "isJob")
			suffix := rapid.StringMatching(`[a-z0-9]{1,12}`).Draw(t, "suffix")
			var name string
			if isJob {
				name = "job-" + suffix
			} else {
				name = suffix
				if strings.HasPrefix(name, "job-") {
					name = "x" + name
				}
			}

			// Skip collisions inside this iteration's scan.
			if _, exists := mfs.mtimes[root+"/"+name]; exists {
				continue
			}

			aged := rapid.Bool().Draw(t, "aged")
			var mtime time.Time
			if aged {
				mtime = now.Add(-ttl - time.Minute)
			} else {
				mtime = now.Add(-ttl + time.Minute)
			}
			mfs.addJobDir(root, name, mtime)
			want[root+"/"+name] = isJob && aged
		}

		obs := &recordingReaperObserver{}
		r := newReaper(outer, root, ttl, mfs, obs)
		if _, err := r.ReapOnce(now); err != nil {
			t.Fatalf("ReapOnce: %v", err)
		}

		got := map[string]bool{}
		for _, p := range mfs.removed {
			got[p] = true
		}

		for p := range got {
			if !want[p] {
				t.Fatalf("removed unexpected path %q", p)
			}
		}
		for p, shouldRemove := range want {
			if shouldRemove && !got[p] {
				t.Fatalf("expected to remove %q, but it survived", p)
			}
		}
	})
}
