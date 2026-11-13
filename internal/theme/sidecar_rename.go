package theme

// Renaming section-identified things; a bad id is refused before anything moves.

import (
	"errors"
	"strconv"
	"strings"
)

var (
	// ErrRenameBadID reports an id outside [a-z0-9_-], IDRuneCap runes, named so the editor can quote the grammar.
	ErrRenameBadID = errors.New("theme: element id must be [a-z0-9_-] and at most " +
		strconv.Itoa(IDRuneCap) + " characters")
	// ErrRenameTaken reports an id another element holds; merging would let line order decide the result.
	ErrRenameTaken = errors.New("theme: another element already has that id")
	// ErrRenameMissing reports a rename of an element this sidecar does not hold.
	ErrRenameMissing = errors.New("theme: no element with that id")
)

// RenameElement changes an id in model and document, or nothing; a new element has no header to move.
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

// CanRenameElement reports why RenameElement would refuse, via renamePlan.
// The editor needs this before recording an undo entry, since a setter cannot say no.
func (s *Sidecar) CanRenameElement(old, next string) error {
	_, _, _, err := s.renamePlan(old, next)
	return err
}

// renamePlan decides every refusal; idx = -1 with nil error means the id is already that.
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
	// A preserved [element.<to>] holds no model element, so a model-only check would bury it.
	if s.doc != nil && s.doc.hasSection(secElementPrefix+to) {
		return -1, from, to, ErrINIDocSectionTaken
	}
	return idx, from, to, nil
}
