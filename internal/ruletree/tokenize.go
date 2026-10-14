package ruletree

type token uint16

const (
	// tokenWildcard represents "*" and matches any set of characters.
	tokenWildcard token = (2 << 7)
	// tokenDomainBoundary represents "||" and matches domain and subdomain roots.
	tokenDomainBoundary token = (2 << 7) + iota
	// tokenSeparator represents "^" and matches any character except a letter, digit, or _-.%.
	// It also matches the end of an address.
	tokenSeparator token = (2 << 7) + iota
	// tokenAnchor represents "|" and matches the beginning or the end of an address.
	tokenAnchor token = (2 << 7) + iota
)

func tokenize(s string) []token {
	return tokenizeInto(s, nil)
}

// Packed-prefix byte encoding (2026-10-13 内存轴 阶段1c): node prefixes are
// stored as bytes, halving the arena footprint versus 2-byte tokens.
//
//	0x00-0xF9  literal byte c
//	0xFA       tokenWildcard   0xFB       tokenSeparator
//	0xFC       tokenAnchor     0xFD       tokenDomainBoundary
//	0xFE       escape: the next byte is a literal 0xFA-0xFF
//	0xFF       unused (never emitted)
//
// Encoding is deterministic, so packed-byte equality == token equality, and
// every prefix starts and ends on token boundaries (no split escapes:
// longestPrefixPacked compares whole token units and returns byte offsets).
const (
	packLitMax    byte = 0xF9 // literals 0x00-0xF9 encode as themselves
	packWildcard  byte = 0xFA
	packSeparator byte = 0xFB
	packAnchor    byte = 0xFC
	packDomain    byte = 0xFD
	packEscape    byte = 0xFE
)

// packTokens encodes tokens into dst[:0]-style packed bytes.
func packTokens(tokens []token, buf []byte) []byte {
	buf = buf[:0]
	for _, t := range tokens {
		if t <= token(packLitMax) {
			buf = append(buf, byte(t))
			continue
		}
		switch t {
		case tokenWildcard:
			buf = append(buf, packWildcard)
		case tokenSeparator:
			buf = append(buf, packSeparator)
		case tokenAnchor:
			buf = append(buf, packAnchor)
		case tokenDomainBoundary:
			buf = append(buf, packDomain)
		default:
			// literal 0xFA-0xFF: escape pair
			buf = append(buf, packEscape, byte(t))
		}
	}
	return buf
}

// tokenizeInto appends s's tokens to buf and returns the extended slice
// (2026-10-13 内存轴): lets Insert reuse a per-Tree scratch buffer instead of
// allocating a fresh slice per pattern. The token values are identical to
// tokenize's.
func tokenizeInto(s string, buf []token) []token {
	tokens := buf
	for i := 0; i < len(s); {
		switch s[i] {
		case '*':
			if len(tokens) == 0 || tokens[len(tokens)-1] != tokenWildcard {
				tokens = append(tokens, tokenWildcard)
			}
		case '|':
			switch {
			case i+1 < len(s) && s[i+1] == '|':
				tokens = append(tokens, tokenDomainBoundary)
				i++
			default:
				tokens = append(tokens, tokenAnchor)
			}
		case '^':
			tokens = append(tokens, tokenSeparator)
		default:
			tokens = append(tokens, token(s[i]))
		}
		i++
	}
	return tokens
}
