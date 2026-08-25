package pve

import (
	"encoding/base64"
	"testing"

	"github.com/donaldgifford/hoomlab/tools/bootstrap/internal/config"
	"github.com/donaldgifford/proxmox-go-sdk/proxmox/nodes"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// certifierFor builds a Certifier with just enough config for the
// pure helpers under test.
func certifierFor(directory string) *Certifier {
	return &Certifier{Cluster: &config.Cluster{
		ACME: config.ACME{Directory: directory},
	}}
}

// TestIssuerMatchesCA pins the staging→production flip heuristic
// (INV-0001 deviation 5): a production-desiring config must reject a
// staging-issued certificate so the flip reopens the order; staging
// and custom CAs express no issuer opinion.
func TestIssuerMatchesCA(t *testing.T) {
	const (
		stagingDir    = "https://acme-staging-v02.api.letsencrypt.org/directory"
		stagingIssuer = "C=US, O=Let's Encrypt, CN=(STAGING) Dastardly Durum YR1"
		prodIssuer    = "C=US, O=Let's Encrypt, CN=R13"
	)
	tests := []struct {
		name      string
		directory string // "" = production default
		issuer    string
		want      bool
	}{
		{"production rejects staging cert", "", stagingIssuer, false},
		{"production accepts production cert", "", prodIssuer, true},
		{"explicit production rejects staging cert", leProductionDirectory, stagingIssuer, false},
		{"staging accepts staging cert", stagingDir, stagingIssuer, true},
		{"staging accepts production cert", stagingDir, prodIssuer, true},
		{"custom CA expresses no opinion", "https://ca.internal/directory", stagingIssuer, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := certifierFor(tt.directory).issuerMatchesCA(tt.issuer); got != tt.want {
				t.Errorf("issuerMatchesCA(%q) with directory %q = %v, want %v",
					tt.issuer, tt.directory, got, tt.want)
			}
		})
	}
}

// TestCertActionFor pins the converge path for a pending certificate
// step (INV-0001 deviation 7): PVE's order refuses while a frontend
// certificate exists and the SDK exposes no force, so the path is
// picked from the existing file — plain order when absent, renew when
// the certificate is right but expiring, delete-then-order when it is
// wrong (CA flip, SAN change).
func TestCertActionFor(t *testing.T) {
	const fqdn = "r740a.shart.sh"
	right := nodes.Certificate{
		Filename: "pveproxy-ssl.pem",
		Issuer:   "C=US, O=Let's Encrypt, CN=R13", SAN: []string{fqdn},
	}
	staging := nodes.Certificate{
		Filename: "pveproxy-ssl.pem",
		Issuer:   "C=US, O=Let's Encrypt, CN=(STAGING) Dastardly Durum YR1", SAN: []string{fqdn},
	}
	foreignSAN := nodes.Certificate{
		Filename: "pveproxy-ssl.pem",
		Issuer:   "CN=whatever", SAN: []string{"other.example"},
	}
	selfSigned := nodes.Certificate{
		Filename: "pve-ssl.pem",
		Issuer:   "O=PVE Cluster Manager CA", SAN: []string{"r740a"},
	}

	tests := []struct {
		name  string
		certs []nodes.Certificate
		want  certAction
	}{
		{"no frontend cert", []nodes.Certificate{selfSigned}, certActionOrder},
		{"right cert expiring", []nodes.Certificate{selfSigned, right}, certActionRenew},
		{"staging cert under production", []nodes.Certificate{selfSigned, staging}, certActionReplace},
		{"foreign SAN", []nodes.Certificate{foreignSAN}, certActionReplace},
	}
	c := certifierFor("") // production default
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.certActionFor(tt.certs, fqdn); got != tt.want {
				t.Errorf("certActionFor = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDecodePluginData pins the structural comparison shape (INV-0001
// deviation 6): trailing newlines, CRLF, blank lines, and line order
// must not affect the decoded map; values containing '=' split on the
// first only.
func TestDecodePluginData(t *testing.T) {
	enc := b64
	tests := []struct {
		name    string
		payload string
		want    map[string]string
	}{
		{"plain", enc("CF_Token=abc"), map[string]string{"CF_Token": "abc"}},
		{"trailing newline", enc("CF_Token=abc\n"), map[string]string{"CF_Token": "abc"}},
		{"crlf and blank lines", enc("CF_Token=abc\r\n\r\n"), map[string]string{"CF_Token": "abc"}},
		{"multi-key any order", enc("B=2\nA=1"), map[string]string{"A": "1", "B": "2"}},
		{"value containing equals", enc("K=a=b"), map[string]string{"K": "a=b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodePluginData(tt.payload)
			if err != nil {
				t.Fatalf("decodePluginData: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("decoded %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("decoded[%q] = %q, want %q", k, got[k], v)
				}
			}
		})
	}

	if _, err := decodePluginData("not-base64!"); err == nil {
		t.Error("decodePluginData accepted invalid base64, want error")
	}
}
