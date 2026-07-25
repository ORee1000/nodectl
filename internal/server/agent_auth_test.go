package server

import (
	"encoding/json"
	"strings"
	"testing"

	"nodectl/internal/database"
)

func TestAgentTokenMatches(t *testing.T) {
	if !agentTokenMatches("secret-token", "secret-token") {
		t.Fatal("matching tokens were rejected")
	}
	for _, token := range []string{"", "secret-tokeN", "secret-token-extra"} {
		if agentTokenMatches("secret-token", token) {
			t.Fatalf("non-matching token %q was accepted", token)
		}
	}
}

func TestNodePoolJSONDoesNotExposeAgentToken(t *testing.T) {
	data, err := json.Marshal(database.NodePool{InstallID: "abc", AgentToken: "do-not-leak"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "do-not-leak") || strings.Contains(string(data), "agent_token") {
		t.Fatalf("NodePool JSON leaked AgentToken: %s", data)
	}
}
