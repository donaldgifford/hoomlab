package talos

import (
	"fmt"
	"slices"
	"strings"

	machcfg "github.com/siderolabs/talos/pkg/machinery/config/config"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"
	"github.com/siderolabs/talos/pkg/machinery/config/encoder"
)

// redacted replaces every secret in a printed diff.
const redacted = "******"

// diffContext is how many unchanged lines surround each change.
const diffContext = 2

// configDoc is one machine-config document in normal form: keyed by
// kind (and name, for named documents) and encoded without comments.
type configDoc struct {
	key  string
	yaml string
}

// normalize decodes a machine config through machinery's loader and
// re-encodes it one document at a time, sorted by key, so formatting,
// comments, and document order never count as drift. Live configs are
// multi-document (v1alpha1 plus the 1.12+ documents), so whole-file
// comparison would be wrong twice over: it would see order, and a
// JSON6902-style patch could not even address them. With redact set,
// every secret is replaced first — the form used for printing only.
func normalize(data []byte, redact bool) ([]configDoc, error) {
	provider, err := configloader.NewFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("load machine config: %w", err)
	}
	if redact {
		provider = provider.RedactSecrets(redacted)
	}
	docs := make([]configDoc, 0, len(provider.Documents()))
	for _, doc := range provider.Documents() {
		key := doc.Kind()
		if named, ok := doc.(machcfg.NamedDocument); ok && named.Name() != "" {
			key += "/" + named.Name()
		}
		out, err := encoder.NewEncoder(doc, encoder.WithComments(encoder.CommentsDisabled)).Encode()
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", key, err)
		}
		docs = append(docs, configDoc{key: key, yaml: string(out)})
	}
	slices.SortFunc(docs, func(a, b configDoc) int { return strings.Compare(a.key, b.key) })
	return docs, nil
}

// configsEqual reports whether two machine configs are semantically
// the same after normalization.
func configsEqual(a, b []byte) (bool, error) {
	na, err := normalize(a, false)
	if err != nil {
		return false, err
	}
	nb, err := normalize(b, false)
	if err != nil {
		return false, err
	}
	return slices.Equal(na, nb), nil
}

// configDiff renders a unified-style diff from live to rendered, per
// document, with secrets redacted. Empty means no difference.
func configDiff(live, rendered []byte) (string, error) {
	nl, err := normalize(live, true)
	if err != nil {
		return "", fmt.Errorf("live: %w", err)
	}
	nr, err := normalize(rendered, true)
	if err != nil {
		return "", fmt.Errorf("rendered: %w", err)
	}
	byKey := func(docs []configDoc) map[string]string {
		m := make(map[string]string, len(docs))
		for _, d := range docs {
			m[d.key] = d.yaml
		}
		return m
	}
	l, r := byKey(nl), byKey(nr)
	keys := make([]string, 0, len(l)+len(r))
	for k := range l {
		keys = append(keys, k)
	}
	for k := range r {
		if _, ok := l[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)

	var b strings.Builder
	for _, k := range keys {
		if l[k] == r[k] {
			continue
		}
		fmt.Fprintf(&b, "@@ %s\n", k)
		b.WriteString(lineDiff(l[k], r[k]))
	}
	return b.String(), nil
}

// diffOp is one line of an edit script: ' ' kept, '-' live only,
// '+' rendered only.
type diffOp struct {
	mark byte
	line string
}

// lineDiff is a minimal LCS line diff from live to rendered, with
// diffContext unchanged lines around each change and "…" marking a
// gap.
func lineDiff(live, rendered string) string {
	return formatOps(editScript(splitLines(live), splitLines(rendered)))
}

// editScript computes the LCS edit script turning x into y.
func editScript(x, y []string) []diffOp {
	// lcs[i][j] is the LCS length of x[i:] and y[j:].
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	ops := make([]diffOp, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) || j < len(y) {
		switch {
		case i < len(x) && j < len(y) && x[i] == y[j]:
			ops = append(ops, diffOp{' ', x[i]})
			i++
			j++
		case i < len(x) && (j == len(y) || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, diffOp{'-', x[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', y[j]})
			j++
		}
	}
	return ops
}

// formatOps prints an edit script, keeping only the lines within
// diffContext of a change.
func formatOps(ops []diffOp) string {
	changed := func(k int) bool {
		for d := max(0, k-diffContext); d <= min(len(ops)-1, k+diffContext); d++ {
			if ops[d].mark != ' ' {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	gap := false
	for k, o := range ops {
		if !changed(k) {
			if !gap {
				b.WriteString("  …\n")
				gap = true
			}
			continue
		}
		gap = false
		b.WriteByte(o.mark)
		b.WriteByte(' ')
		b.WriteString(o.line)
		b.WriteByte('\n')
	}
	return b.String()
}

// splitLines splits s into lines without a trailing empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
