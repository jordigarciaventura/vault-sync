package bitwarden

import (
	"fmt"
	"path"
)

// Representation is how a logical file is stored in the vault.
type representation int

const (
	// reprNote: a Secure Note named after the logical filename; the file
	// contents are the note body.
	reprNote representation = iota + 1
	// reprAttachment: a file attached to a vault item.
	reprAttachment
)

func (r representation) String() string {
	switch r {
	case reprNote:
		return "secure note"
	case reprAttachment:
		return "attachment"
	default:
		return "unknown"
	}
}

// match is one candidate resource for a logical filename.
type match struct {
	repr       representation
	item       item
	attachment attachment // set for reprAttachment
}

func (m match) describe() string {
	if m.repr == reprAttachment {
		return fmt.Sprintf("attachment %q on item %q (%s)", m.attachment.FileName, m.item.Name, m.item.ID)
	}
	return fmt.Sprintf("secure note %q (%s)", m.item.Name, m.item.ID)
}

// resolve finds every resource in a project folder matching filename.
//
// The rules, applied to every item in the folder, are:
//
//  1. An item whose name equals filename and that carries an attachment named
//     filename or path.Base(filename) resolves to that attachment.
//  2. Otherwise, a Secure Note whose name equals filename resolves to its
//     note body (which may be empty).
//  3. Independently of the item's name, any attachment whose file name equals
//     filename exactly is a match, so one item can hold several files.
//
// An item can contribute several matches (e.g. two attachments with the same
// name); callers must treat more than one match as ambiguous.
func resolve(items []item, filename string) []match {
	base := path.Base(filename)
	var matches []match
	for _, it := range items {
		named := it.Name == filename
		matchedAttachment := false
		for _, a := range it.Attachments {
			if a.FileName == filename || (named && a.FileName == base) {
				matches = append(matches, match{repr: reprAttachment, item: it, attachment: a})
				matchedAttachment = true
			}
		}
		if named && !matchedAttachment && it.Type == itemTypeSecureNote {
			matches = append(matches, match{repr: reprNote, item: it})
		}
	}
	return matches
}

// namedItems lists the ids of items named filename. It is used to explain a
// not-found error when such items exist but hold no usable representation.
func namedItems(items []item, filename string) []string {
	var out []string
	for _, it := range items {
		if it.Name == filename {
			out = append(out, it.ID)
		}
	}
	return out
}
