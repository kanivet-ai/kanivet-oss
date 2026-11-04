package certs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math"
	"net"
	"strings"
	"time"
)

// ParsedBundle is a PEM bundle decoded into x509 certificates, leaf first.
type ParsedBundle struct {
	Certs []*x509.Certificate
}

// ParsePEM decodes every CERTIFICATE block of data. Non-certificate blocks
// (keys included) are skipped without being retained.
func ParsePEM(data []byte) (*ParsedBundle, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("empty certificate data")
	}
	var out ParsedBundle
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		out.Certs = append(out.Certs, c)
	}
	if len(out.Certs) == 0 {
		return nil, errors.New("no PEM certificate found")
	}
	return &out, nil
}

// bucket returns the expiry Status and whole days left (negative once expired).
func bucket(notAfter, now time.Time) (Status, int) {
	d := notAfter.Sub(now)
	days := int(math.Floor(d.Hours() / 24))
	switch {
	case d <= 0:
		return StatusExpired, days
	case days < CriticalDays:
		return StatusCritical, days
	case days < WarningDays:
		return StatusWarning, days
	default:
		return StatusOK, days
	}
}

func isSelfSigned(c *x509.Certificate) bool {
	if !bytes.Equal(c.RawIssuer, c.RawSubject) {
		return false
	}
	return c.CheckSignatureFrom(c) == nil
}

func keyInfo(c *x509.Certificate) (string, int) {
	switch k := c.PublicKey.(type) {
	case *rsa.PublicKey:
		return "RSA", k.N.BitLen()
	case *ecdsa.PublicKey:
		return "ECDSA", k.Curve.Params().BitSize
	case ed25519.PublicKey:
		return "Ed25519", 256
	default:
		return c.PublicKeyAlgorithm.String(), 0
	}
}

func sans(c *x509.Certificate) []string {
	out := make([]string, 0, len(c.DNSNames)+len(c.IPAddresses))
	out = append(out, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		out = append(out, ip.String())
	}
	for _, u := range c.URIs {
		out = append(out, u.String())
	}
	out = append(out, c.EmailAddresses...)
	return out
}

// fillFromBundle populates the certificate fields of cert from the bundle's leaf.
func fillFromBundle(cert *Certificate, b *ParsedBundle, now time.Time) {
	leaf := b.Certs[0]
	cert.Subject = leaf.Subject.String()
	cert.CommonName = leaf.Subject.CommonName
	cert.SANs = sans(leaf)
	cert.Issuer = leaf.Issuer.String()
	cert.IssuerCommonName = leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 {
		cert.IssuerOrganization = leaf.Issuer.Organization[0]
	}
	nb, na := leaf.NotBefore, leaf.NotAfter
	cert.NotBefore, cert.NotAfter = &nb, &na
	cert.Status, cert.DaysLeft = bucket(na, now)
	cert.KeyAlgorithm, cert.KeySize = keyInfo(leaf)
	cert.SignatureAlgorithm = leaf.SignatureAlgorithm.String()
	cert.SelfSigned = isSelfSigned(leaf)
	cert.IsCA = leaf.IsCA
	if leaf.SerialNumber != nil {
		cert.SerialNumber = strings.ToUpper(leaf.SerialNumber.Text(16))
	}
	sum := sha256.Sum256(leaf.Raw)
	cert.FingerprintSHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	cert.Chain = make([]ChainEntry, 0, len(b.Certs))
	for _, c := range b.Certs {
		cert.Chain = append(cert.Chain, ChainEntry{
			Subject:    displayName(c.Subject.CommonName, c.Subject.String()),
			Issuer:     displayName(c.Issuer.CommonName, c.Issuer.String()),
			NotBefore:  c.NotBefore,
			NotAfter:   c.NotAfter,
			IsCA:       c.IsCA,
			SelfSigned: isSelfSigned(c),
		})
	}
}

func displayName(cn, full string) string {
	if cn != "" {
		return cn
	}
	return full
}

// HostCovered reports whether host is matched by one of sans, honouring a
// single leading wildcard label ("*.example.com" covers "a.example.com" but
// not "example.com" or "a.b.example.com"). Comparison is case-insensitive.
func HostCovered(host string, sans []string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" {
		return true
	}
	// An Ingress host may itself be a wildcard; it is covered only by the
	// same wildcard.
	for _, s := range sans {
		s = strings.TrimSuffix(strings.ToLower(s), ".")
		if s == host {
			return true
		}
		if net.ParseIP(host) != nil {
			continue
		}
		if strings.HasPrefix(s, "*.") && !strings.HasPrefix(host, "*.") {
			suffix := s[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) {
				label := strings.TrimSuffix(host, suffix)
				if label != "" && !strings.Contains(label, ".") {
					return true
				}
			}
		}
	}
	return false
}
