package ai

import "regexp"

// answerRe matches `{"answer": "..."}` including escapes inside the value.
var answerRe = regexp.MustCompile(`(?s)\{\s*"(?:answer|anwser)"\s*:\s*"(.*?)"[\s\S]*?\}`)
