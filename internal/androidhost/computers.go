package androidhost

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/YuanZhe-99/AndroFuse/internal/companion"
)

// PairedComputer is one computer paired with this phone: its own id (the id in its mesh
// hostname) and name, a name given here, the SHA-256 of its token (the token itself is kept only
// by that computer), and when it paired and last connected.
type PairedComputer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Alias     string    `json:"alias,omitempty"`
	TokenHash string    `json:"token_hash"`
	PairedAt  time.Time `json:"paired_at"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
}

// shownName is what the phone calls c: the name given here, else the computer's own.
func (c PairedComputer) shownName() string {
	if c.Alias != "" {
		return c.Alias
	}
	return c.Name
}

// tokenHash is how a computer's token is kept on the phone.
func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// issueToken gives the computer req names a token of its own once its pairing was allowed,
// replacing its earlier one (Handler.IssueToken).
// Side effects: saves companion.json; audit log.
func (h *Host) issueToken(req companion.PairRequest) (string, error) {
	tok, err := companion.GeneratePairingToken()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(req.ComputerID)
	if id == "" {
		id = "name:" + strings.TrimSpace(req.ClientName) // a computer from before computer ids
	}
	now := time.Now()
	h.mu.Lock()
	alias := ""
	h.cfg.Computers = slices.DeleteFunc(h.cfg.Computers, func(c PairedComputer) bool {
		if c.ID == id {
			alias = c.Alias // pairing again keeps the name given here
			return true
		}
		return false
	})
	h.cfg.Computers = append(h.cfg.Computers, PairedComputer{ID: id, Name: strings.TrimSpace(req.ClientName), Alias: alias, TokenHash: tokenHash(tok), PairedAt: now, LastSeen: now})
	h.appendAuditLocked(fmt.Sprintf("paired with %q", req.ClientName))
	h.saveConfigLocked()
	h.mu.Unlock()
	h.notify()
	return tok, nil
}

// checkToken reports whether an auth request's token is a paired computer's, and records that it
// connected (Handler.CheckToken). The phone's one shared token from before per-computer pairings
// is still accepted, and that computer then joins the list under the token it holds.
// Side effects: saves companion.json when the list changed or at most once a minute.
func (h *Host) checkToken(req companion.AuthRequest) bool {
	hash := tokenHash(req.Token)
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.cfg.Computers {
		c := &h.cfg.Computers[i]
		if subtle.ConstantTimeCompare([]byte(c.TokenHash), []byte(hash)) != 1 {
			continue
		}
		save := now.Sub(c.LastSeen) > time.Minute
		if name := strings.TrimSpace(req.ClientName); name != "" && name != c.Name {
			c.Name, save = name, true
		}
		c.LastSeen = now
		if save {
			h.saveConfigLocked()
			go h.notify()
		}
		return true
	}
	if h.cfg.Token == "" || subtle.ConstantTimeCompare([]byte(req.Token), []byte(h.cfg.Token)) != 1 {
		return false
	}
	id := strings.TrimSpace(req.ComputerID)
	if id == "" {
		id = "name:" + strings.TrimSpace(req.ClientName)
	}
	h.cfg.Computers = append(h.cfg.Computers, PairedComputer{ID: id, Name: strings.TrimSpace(req.ClientName), TokenHash: hash, PairedAt: now, LastSeen: now})
	h.saveConfigLocked()
	go h.notify()
	return true
}

// Computers returns the paired computers, most recently connected first.
func (h *Host) Computers() []PairedComputer {
	h.mu.Lock()
	out := slices.Clone(h.cfg.Computers)
	h.mu.Unlock()
	slices.SortFunc(out, func(a, b PairedComputer) int { return b.LastSeen.Compare(a.LastSeen) })
	return out
}

// RenameComputer names the paired computer id on this phone; "" goes back to its own name.
// Side effects: saves companion.json.
func (h *Host) RenameComputer(id, alias string) {
	h.mu.Lock()
	for i := range h.cfg.Computers {
		if h.cfg.Computers[i].ID == id {
			h.cfg.Computers[i].Alias = strings.TrimSpace(alias)
		}
	}
	h.saveConfigLocked()
	h.mu.Unlock()
	h.notify()
}

// ForgetComputer unpairs the computer id: its token stops working at once, and when it held the
// old shared token, that token is replaced too, so no computer can use it any more.
// Side effects: saves companion.json; may rotate the shared token; audit log.
func (h *Host) ForgetComputer(id string) {
	h.mu.Lock()
	var gone PairedComputer
	h.cfg.Computers = slices.DeleteFunc(h.cfg.Computers, func(c PairedComputer) bool {
		if c.ID == id {
			gone = c
			return true
		}
		return false
	})
	shared := h.cfg.Token != "" && gone.TokenHash == tokenHash(h.cfg.Token)
	h.appendAuditLocked(fmt.Sprintf("forgot computer %q", gone.shownName()))
	h.saveConfigLocked()
	h.mu.Unlock()
	if shared {
		_, _ = h.RotateToken()
	}
	h.notify()
}
