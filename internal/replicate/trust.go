package replicate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Identity is this clone's ed25519 keypair. The private key never leaves <git-common-dir>/wbi/.
type Identity struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

func (i Identity) PubB64() string { return base64.StdEncoding.EncodeToString(i.Pub) }

// Fingerprint is a short, human-comparable id for a base64 public key.
func Fingerprint(pubB64 string) string {
	b, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil {
		return "invalid"
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:6])
}

// LoadIdentity reads (or creates, 0600) the clone's signing key stored next to the state database.
func LoadIdentity(stateDir string) (Identity, error) {
	path := filepath.Join(stateDir, "identity.ed25519")
	if b, err := os.ReadFile(path); err == nil {
		if seed, err := hex.DecodeString(strings.TrimSpace(string(b))); err == nil && len(seed) == ed25519.SeedSize {
			priv := ed25519.NewKeyFromSeed(seed)
			return Identity{Priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
		}
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return Identity{}, err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return Identity{}, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		return Identity{}, err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return Identity{Priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
}

// Member is one person's key in .wbi/team.json.
type Member struct {
	Name string `json:"name"`
	Key  string `json:"key"` // base64 ed25519 public key
}

// Team is the committed allow-list. Adding a member is a pull request, so authorization is reviewed like code.
type Team struct {
	Version int      `json:"version"`
	Enforce bool     `json:"enforce"`
	Members []Member `json:"members"`
}

// Policy is the team file as the verifier uses it.
type Policy struct {
	Enforce bool
	Members map[string]string // base64 key → name
	Hash    string            // changes when the policy file changes, which triggers a full re-verification
}

// LoadTeam reads <root>/.wbi/team.json (missing is fine: no policy).
func LoadTeam(root string) (Team, []byte) {
	b, err := os.ReadFile(filepath.Join(root, ".wbi", "team.json"))
	var t Team
	if err != nil || json.Unmarshal(b, &t) != nil {
		return Team{Version: 1}, nil
	}
	return t, b
}

func LoadPolicy(root string) Policy {
	t, raw := LoadTeam(root)
	p := Policy{Enforce: t.Enforce, Members: map[string]string{}}
	for _, m := range t.Members {
		p.Members[m.Key] = m.Name
	}
	if raw != nil {
		h := sha256.Sum256(raw)
		p.Hash = hex.EncodeToString(h[:8])
	}
	return p
}

type header struct {
	V     int    `json:"v"`
	Actor string `json:"actor"`
	Key   string `json:"key"`
	Sig   string `json:"sig"`
}

// sealBatch prefixes the body with a header carrying the signer's public key and a signature over the body.
func sealBatch(id Identity, actor string, body []byte) []byte {
	h, _ := json.Marshal(header{V: 1, Actor: actor, Key: id.PubB64(), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(id.Priv, body))})
	return append(append(h, '\n'), body...)
}

// Verdict is the outcome of checking one published batch against the policy.
type Verdict int

const (
	Accept Verdict = iota
	RejectForged
	RejectUnauthorized
	RejectUnsigned
)

// openBatch splits a file into header and body and decides whether the policy accepts it.
func openBatch(file string, pol Policy) (body string, key string, v Verdict) {
	nl := strings.IndexByte(file, '\n')
	if nl < 0 {
		return file, "", unsignedVerdict(pol)
	}
	var h header
	if json.Unmarshal([]byte(file[:nl]), &h) != nil || h.V == 0 || h.Key == "" {
		return file, "", unsignedVerdict(pol) // legacy / unsigned batch: the first line is already an event
	}
	body = file[nl+1:]
	pub, err1 := base64.StdEncoding.DecodeString(h.Key)
	sig, err2 := base64.StdEncoding.DecodeString(h.Sig)
	if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), []byte(body), sig) {
		return body, h.Key, RejectForged // a bad signature is never accepted, enforcing or not
	}
	if pol.Enforce {
		if _, ok := pol.Members[h.Key]; !ok {
			return body, h.Key, RejectUnauthorized
		}
	}
	return body, h.Key, Accept
}

func unsignedVerdict(pol Policy) Verdict {
	if pol.Enforce {
		return RejectUnsigned
	}
	return Accept
}
