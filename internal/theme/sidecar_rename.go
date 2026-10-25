package theme

// RENAMING WHAT THE FORMAT IDENTIFIES BY ITS SECTION NAME (v1.90.0 W8).
// THE ID GRAMMAR IS THE READER'S OWN (validID). An id that would not parse back
// is refused BEFORE anything moves, so a rename can never be the reason a theme
// stops loading — the same precondition Sidecar.Bytes states for the whole model.

import (
	"errors"
	"strconv"
	"strings"
)

var (
	// ErrRenameBadID reports an id outside the reader's own grammar
	// ([a-z0-9_-], IDRuneCap runes). NAMED so the editor can quote the grammar
	// rather than saying "invalid" (design §3.3: name the offender, name the
	// limit, offer the fix).
	ErrRenameBadID = errors.New("theme: element id must be [a-z0-9_-] and at most " +
		strconv.Itoa(IDRuneCap) + " characters")
	// ErrRenameTaken reports an id another element already holds. Refused rather
	// than merged: two elements under one section is one element with the other's
	// leftovers, decided by line order.
	ErrRenameTaken = errors.New("theme: another element already has that id")
	// ErrRenameMissing reports a rename of an element this sidecar does not hold.
	ErrRenameMissing = errors.New("theme: no element with that id")
)

// RenameElement changes an element's id, in the model and in the lossless
// carrier, or changes nothing at all.
// A document that has never seen this element — one the editor authored this
// session — has no header to move, and that is a success rather than a failure:
// the writer creates the section under the new name on the next save, which is
// what it would have done anyway.
func (s *Sidecar) RenameElement(old, next string) error {
	idx, from, to, err := s.renamePlan(old, next)
	if err != nil || idx < 0 {
		return err // idx < 0 with no error is "already called that": nothing to do
	}
	if s.doc != nil {
		if err := s.doc.RenameSection(secElementPrefix+from, secElementPrefix+to); err != nil &&
			!errors.Is(err, ErrINIDocNoSection) {
			return err
		}
	}
	s.Elements[idx].ID = to
	return nil
}

// CanRenameElement reports the reason RenameElement would refuse, without moving
// anything.
// IT IS NOT A SECOND COPY OF THE RULES — both entry points are renamePlan — and
// that is the whole reason it exists rather than the caller re-deriving "is this
// id free?" for itself. The editor needs the answer BEFORE it records an undo
// entry: a setter cannot say no (it has no return value and the op is already on
// the ring by the time it runs), so a refusal discovered there would be either a
// silent no-op or an undo step that undoes nothing.
func (s *Sidecar) CanRenameElement(old, next string) error {
	_, _, _, err := s.renamePlan(old, next)
	return err
}

// renamePlan decides every refusal. It returns the model index to move, the
// trimmed pair, and the reason it cannot — or idx = -1 with a nil error for the
// "the id is already that" no-op.
func (s *Sidecar) renamePlan(old, next string) (idx int, from, to string, err error) {
	if s == nil {
		return -1, "", "", ErrRenameMissing
	}
	from, to = strings.TrimSpace(old), strings.TrimSpace(next)
	if !validID(to) {
		return -1, from, to, ErrRenameBadID
	}
	idx = -1
	for i := range s.Elements {
		if s.Elements[i].ID == from {
			idx = i
			continue
		}
		if s.Elements[i].ID == to {
			return -1, from, to, ErrRenameTaken
		}
	}
	if idx < 0 {
		return -1, from, to, ErrRenameMissing
	}
	if from == to {
		return -1, from, to, nil
	}
	// THE CARRIER'S OWN COLLISION, checked here rather than discovered halfway
	// through: a `[element.<to>]` this build preserved-and-ignored (an unknown kind
	// from a newer client, an id this reader could not parse) holds no model
	// element, so a model-only check would happily rename another element on top of
	// somebody's forward-compatible section and bury it.
	if s.doc != nil && s.doc.hasSection(secElementPrefix+to) {
		return -1, from, to, ErrINIDocSectionTaken
	}
	return idx, from, to, nil
}
