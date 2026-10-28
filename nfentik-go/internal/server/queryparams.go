package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// rawQueryValues returns the raw (still percent-encoded) values for key. It is
// used when form decoding would turn a literal "+" into a space.
func rawQueryValues(rawQuery, key string) []string {
	var out []string
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, "=", 2)
		name, err := url.PathUnescape(pair[0])
		if err != nil || name != key {
			continue
		}
		value := ""
		if len(pair) == 2 {
			value = pair[1]
		}
		out = append(out, value)
	}
	return out
}

type queryByte struct {
	value byte
	plus  bool // raw '+' that standard form decoding would turn into a space
}

// decodeQueryValue keeps a raw "+" when it separates math operands. Standard
// form decoding treats every "+" as a space, so a client that sends
// "q=1+1=" without percent-encoding would otherwise query "1 1=".
//
// Decoding is done over percent-decoded bytes so a percent escape such as %89
// is never mistaken for an ASCII digit when deciding what surrounds a '+'.
func decodeQueryValue(raw string) string {
	tokens := make([]queryByte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] == '%' && i+2 < len(raw) {
			if v, err := strconv.ParseUint(raw[i+1:i+3], 16, 8); err == nil {
				tokens = append(tokens, queryByte{value: byte(v)})
				i += 3
				continue
			}
		}
		if raw[i] == '+' {
			tokens = append(tokens, queryByte{plus: true})
			i++
			continue
		}
		tokens = append(tokens, queryByte{value: raw[i]})
		i++
	}
	var b strings.Builder
	for i, token := range tokens {
		if !token.plus {
			b.WriteByte(token.value)
			continue
		}
		if i > 0 && i+1 < len(tokens) && protectMathPlus(tokens, i) {
			b.WriteByte('+')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func protectMathPlus(tokens []queryByte, i int) bool {
	prev, prevOK := tokenByte(tokens, i-1)
	next, nextOK := tokenByte(tokens, i+1)
	if !prevOK || !nextOK {
		return false
	}
	if isASCIIDigit(prev) && isASCIIDigit(next) {
		return true
	}
	if isASCIIDigit(prev) && isBracket(next) {
		return true
	}
	if isASCIIDigit(next) && isBracket(prev) {
		return true
	}
	if isASCIIDigit(prev) && isSingleLetter(tokens, i+1) {
		return true
	}
	if isASCIIDigit(next) && isSingleLetter(tokens, i-1) {
		return true
	}
	return isSingleLetter(tokens, i-1) && isSingleLetter(tokens, i+1)
}

func tokenByte(tokens []queryByte, idx int) (byte, bool) {
	if idx < 0 || idx >= len(tokens) || tokens[idx].plus {
		return 0, false
	}
	return tokens[idx].value, true
}

func isSingleLetter(tokens []queryByte, idx int) bool {
	if idx < 0 || idx >= len(tokens) || tokens[idx].plus || !isASCIILetter(tokens[idx].value) {
		return false
	}
	if prev, ok := tokenByte(tokens, idx-1); ok && isASCIILetter(prev) {
		// A letter followed by another letter is part of a word, not a
		// single-letter variable. A digit may be a coefficient (2x).
		return false
	}
	if next, ok := tokenByte(tokens, idx+1); ok && isASCIIAlnum(next) {
		return false
	}
	return true
}

func isASCIIAlnum(b byte) bool {
	return isASCIIDigit(b) || isASCIILetter(b)
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

func isBracket(b byte) bool {
	return b == '(' || b == ')' || b == '[' || b == ']'
}

// queryValue reads a query parameter with math-aware "+" handling. Ordinary
// text still follows form semantics: "+" between words becomes a space.
func queryValue(r *http.Request, key string) string {
	if raw := firstQueryValue(rawQueryValues(r.URL.RawQuery, key)); raw != "" {
		return decodeQueryValue(raw)
	}
	return firstQueryValue(r.URL.Query()[key])
}
