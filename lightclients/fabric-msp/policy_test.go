package fabric_msp_test

// Teste de cripto, não estrutural: gera uma MSP de teste
// (cert raiz + chave ECDSA reais, via bccsp/sw real), uma política de
// endosso via policydsl real ("OR('Org1MSP.member')"), assina uma
// mensagem com a chave privada e confirma que VerifyEndorsedMessage
// aceita a assinatura válida e rejeita uma mensagem adulterada.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/hyperledger/fabric-lib-go/bccsp/factory"
	apiv2msp "github.com/hyperledger/fabric-protos-go-apiv2/msp"
	apiv2peer "github.com/hyperledger/fabric-protos-go-apiv2/peer"
	"github.com/hyperledger/fabric/common/policydsl"
	fabricmsp "github.com/hyperledger/fabric/msp"
	"google.golang.org/protobuf/proto"

	fmsp "github.com/hyperledger-labs/yui-relayer/lightclients/fabric-msp"
)

func newCA(t *testing.T) (certPEM, keyPEM []byte, cert *x509.Certificate, key *ecdsa.PrivateKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Org1MSP-ca", Organization: []string{"Org1MSP"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, parsed, priv
}

// newLeafSignedByCA gera uma identidade folha assinada pela CA
func newLeafSignedByCA(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Org1MSP-admin", Organization: []string{"Org1MSP"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// buildMSPConfigBytes monta um msp.MSPConfig (FABRIC/X.509) real,
// serializado — o mesmo formato que MSPInfo.Config carrega. rootCertPEM
// é a CA; adminCertPEM/adminKeyPEM são a identidade folha que assina.
func buildMSPConfigBytes(t *testing.T, mspID string, rootCertPEM, adminCertPEM, adminKeyPEM []byte) []byte {
	t.Helper()
	fabricConfig := &apiv2msp.FabricMSPConfig{
		Name:      mspID,
		RootCerts: [][]byte{rootCertPEM},
		Admins:    [][]byte{adminCertPEM},
		SigningIdentity: &apiv2msp.SigningIdentityInfo{
			PublicSigner: adminCertPEM,
			PrivateSigner: &apiv2msp.KeyInfo{
				KeyIdentifier: "test-key",
				KeyMaterial:   adminKeyPEM,
			},
		},
	}
	fabricConfigBytes, err := proto.Marshal(fabricConfig)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &apiv2msp.MSPConfig{Type: int32(fabricmsp.FABRIC), Config: fabricConfigBytes}
	cfgBytes, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfgBytes
}

// newSigningIdentity monta uma MSP real (bccspmsp) com capacidade de
// assinatura a partir do MSPConfig, e devolve sua identidade de
// assinatura padrão.
func newSigningIdentity(t *testing.T, mspConfigBytes []byte) fabricmsp.SigningIdentity {
	t.Helper()
	cryptoProvider, err := (&factory.SWFactory{}).Get(factory.GetDefaultOpts())
	if err != nil {
		t.Fatal(err)
	}
	opts := fabricmsp.Options[fabricmsp.ProviderTypeToString(fabricmsp.FABRIC)]
	m, err := fabricmsp.New(opts, cryptoProvider)
	if err != nil {
		t.Fatal(err)
	}
	var conf apiv2msp.MSPConfig
	if err := proto.Unmarshal(mspConfigBytes, &conf); err != nil {
		t.Fatal(err)
	}
	if err := m.Setup(&conf); err != nil {
		t.Fatal(err)
	}
	sid, err := m.GetDefaultSigningIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestVerifyEndorsedMessage_RealCrypto(t *testing.T) {
	rootCertPEM, _, rootCert, rootKey := newCA(t)
	adminCertPEM, adminKeyPEM := newLeafSignedByCA(t, rootCert, rootKey)
	mspConfigBytes := buildMSPConfigBytes(t, "Org1MSP", rootCertPEM, adminCertPEM, adminKeyPEM)

	sid := newSigningIdentity(t, mspConfigBytes)

	msg := []byte("hello fabric-msp")
	sig, err := sid.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	idBytes, err := sid.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	proof := fmsp.MessageProof{
		Identities: [][]byte{idBytes},
		Signatures: [][]byte{sig},
	}

	mspInfos := fmsp.NewMSPInfos([]fmsp.MSPInfo{fmsp.NewMSPInfo("Org1MSP", mspConfigBytes, nil)})
	configs, err := mspInfos.GetMSPPBConfigs()
	if err != nil {
		t.Fatal(err)
	}

	envelope, err := policydsl.FromString("OR('Org1MSP.member')")
	if err != nil {
		t.Fatal(err)
	}
	ap := &apiv2peer.ApplicationPolicy{Type: &apiv2peer.ApplicationPolicy_SignaturePolicy{SignaturePolicy: envelope}}
	policyBytes, err := proto.Marshal(ap)
	if err != nil {
		t.Fatal(err)
	}

	if err := fmsp.VerifyEndorsedMessage(policyBytes, proof, msg, configs); err != nil {
		t.Fatalf("esperava que a assinatura válida fosse aceita, erro: %v", err)
	}

	tampered := []byte("mensagem diferente da assinada")
	if err := fmsp.VerifyEndorsedMessage(policyBytes, proof, tampered, configs); err == nil {
		t.Fatal("esperava que a mensagem adulterada fosse rejeitada, mas passou")
	}
}
