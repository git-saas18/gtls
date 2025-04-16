package gtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/caddyserver/certmagic"
)

var Https = certmagic.HTTPS

func MagicTLS(domainNames []string) (*tls.Config, error) {
	return certmagic.TLS(domainNames)
}

//go:embed ssl/gospider.crt
var CrtFile []byte

//go:embed ssl/gospider.key
var KeyFile []byte

func SplitHostPort(address string) (string, int, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, err
	}
	if 1 > portNum || portNum > 0xffff {
		return "", 0, errors.New("port number out of range " + port)
	}
	return host, portNum, nil
}

func ParseHost(host string) (net.IP, int) {
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, 4
		} else if ip6 := ip.To16(); ip6 != nil {
			return ip6, 6
		}
	}
	return nil, 0
}

func VerifyProxy(proxyUrl string) (*url.URL, error) {
	proxy, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}
	switch proxy.Scheme {
	case "http", "https", "socks5", "socks5h":
		return proxy, nil
	default:
		return nil, errors.New("unsupported proxy scheme: " + proxy.Scheme)
	}
}

func GetServerName(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func CreateRootCert(key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	beforeDate, err := time.ParseInLocation(time.DateOnly, "2023-03-20", time.Local)
	if err != nil {
		return nil, err
	}
	afterDate, err := time.ParseInLocation(time.DateOnly, "3023-03-20", time.Local)
	if err != nil {
		return nil, err
	}
	rootCsr := &x509.Certificate{
		Version:      3,
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			Country:            []string{"CN"},
			Province:           []string{"Shanghai"},
			Locality:           []string{"Shanghai"},
			Organization:       []string{"GoSpider"},
			OrganizationalUnit: []string{"GoSpiderProxy"},
			CommonName:         "Gospider Root CA",
		},
		NotBefore:             beforeDate,
		NotAfter:              afterDate,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDer, err := x509.CreateCertificate(rand.Reader, rootCsr, rootCsr, key.Public(), key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(rootDer)
}

func CreateCertKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func CreateCertWithCN(rootCert *x509.Certificate, key *ecdsa.PrivateKey, commonName string) (*x509.Certificate, error) {
	csr := &x509.Certificate{
		Version:               3,
		SerialNumber:          big.NewInt(time.Now().Unix()),
		Subject:               rootCert.Subject,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1000, 0, 0),
		BasicConstraintsValid: true,
		IsCA:                  false,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	csr.IPAddresses = []net.IP{}
	if commonName != "" {
		if ip, ipType := ParseHost(commonName); ipType == 0 {
			csr.Subject.CommonName = commonName
			csr.DNSNames = []string{commonName}
		} else {
			csr.IPAddresses = append(csr.IPAddresses, ip)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, csr, rootCert, key.Public(), key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func CreateCertWithCert(rootCert *x509.Certificate, key *ecdsa.PrivateKey, preCert *x509.Certificate) (*x509.Certificate, error) {
	if preCert.DNSNames == nil && preCert.Subject.CommonName != "" {
		preCert.DNSNames = []string{preCert.Subject.CommonName}
	}
	rootCert.Subject.CommonName = preCert.Subject.CommonName
	csr := &x509.Certificate{
		Version:               3,
		SerialNumber:          big.NewInt(time.Now().Unix()),
		Subject:               rootCert.Subject,
		DNSNames:              preCert.DNSNames,
		IPAddresses:           preCert.IPAddresses,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1000, 0, 0),
		BasicConstraintsValid: true,
		IsCA:                  false,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if len(preCert.DNSNames) > 0 {
		csr.Subject.CommonName = preCert.DNSNames[0]
	}
	der, err := x509.CreateCertificate(rand.Reader, csr, rootCert, key.Public(), key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func CreateProxyCertWithName(serverName string) (tlsCert tls.Certificate, err error) {
	crt, err := LoadCert(CrtFile)
	if err != nil {
		return tlsCert, err
	}
	key, err := LoadCertKey(KeyFile)
	if err != nil {
		return tlsCert, err
	}
	cert, err := CreateCertWithCN(crt, key, serverName)
	if err != nil {
		return tlsCert, err
	}
	return CreateTlsCert(cert, key)
}

func CreateProxyCertWithCert(crt *x509.Certificate, key *ecdsa.PrivateKey, preCert *x509.Certificate) (tlsCert tls.Certificate, err error) {
	if crt == nil {
		crt, err = LoadCert(CrtFile)
		if err != nil {
			return tlsCert, err
		}
	}
	if key == nil {
		key, err = LoadCertKey(KeyFile)
		if err != nil {
			return tlsCert, err
		}
	}
	cert, err := CreateCertWithCert(crt, key, preCert)
	if err != nil {
		return tlsCert, err
	}
	return CreateTlsCert(cert, key)
}

func CreateTlsCert(cert *x509.Certificate, key *ecdsa.PrivateKey) (tls.Certificate, error) {
	keyFile, err := GetCertKeyData(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(GetCertData(cert), keyFile)
}

func GetCertData(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func GetCertKeyData(key *ecdsa.PrivateKey) ([]byte, error) {
	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), nil
}

func LoadCertKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	return x509.ParseECPrivateKey(block.Bytes)
}

func LoadCert(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	return x509.ParseCertificate(block.Bytes)
}

type AddrType int

func (at AddrType) String() string {
	switch at {
	case AutoIp:
		return "AutoIp"
	case Ipv4:
		return "Ipv4"
	case Ipv6:
		return "Ipv6"
	case UnknownIp:
		return "UnknownIp"
	default:
		return "Undefined"
	}
}

const (
	AutoIp AddrType = iota
	Ipv4
	Ipv6
	UnknownIp
)

func ParseIp(ip net.IP) AddrType {
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return Ipv4
		} else if ip6 := ip.To16(); ip6 != nil {
			return Ipv6
		}
	}
	return UnknownIp
}

func GetHost(addrTypes ...AddrType) net.IP {
	hosts := GetHosts(addrTypes...)
	if len(hosts) == 0 {
		return nil
	} else {
		return hosts[0]
	}
}

func GetHosts(addrTypes ...AddrType) []net.IP {
	var addrType AddrType
	if len(addrTypes) > 0 {
		addrType = addrTypes[0]
	}
	var result []net.IP
	lls, err := net.InterfaceAddrs()
	if err != nil {
		return result
	}
	for _, ll := range lls {
		mm, ok := ll.(*net.IPNet)
		if ok && mm.IP.IsPrivate() {
			if addrType == 0 || ParseIp(mm.IP) == addrType {
				result = append(result, mm.IP)
			}
		}
	}
	return result
}
