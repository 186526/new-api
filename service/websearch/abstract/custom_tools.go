package abstract

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

const CustomToolsProtocol = "exa-v1"

var (
	ErrCustomToolConfig  = errors.New("invalid custom tool configuration")
	customToolPath       = regexp.MustCompile(`^/[A-Za-z0-9._~/-]+$`)
	customToolProtection = &common.SSRFProtection{AllowedPorts: []int{443}}
)

type CustomTools struct {
	Search *CustomToolOperation `json:"search,omitempty"`
	Fetch  *CustomToolOperation `json:"fetch,omitempty"`
}

type CustomToolOperation struct {
	Protocol   string `json:"protocol"`
	Path       string `json:"path"`
	Credential string `json:"credential"`
	Proof      string `json:"proof,omitempty"`
}

func DecodeCustomTools(raw *string) (CustomTools, error) {
	var tools CustomTools
	if raw != nil && *raw != "" {
		if err := json.Unmarshal([]byte(*raw), &tools); err != nil {
			return CustomTools{}, ErrCustomToolConfig
		}
	}
	return tools, nil
}

func (tools *CustomTools) Operation(contract Contract) **CustomToolOperation {
	switch {
	case contract.IsSearch():
		return &tools.Search
	case contract.IsFetch():
		return &tools.Fetch
	default:
		return nil
	}
}

func (operation *CustomToolOperation) Endpoint(base string) (string, error) {
	if operation == nil || operation.Protocol != CustomToolsProtocol || !isCustomToolPath(operation.Path) || operation.Path == "/" {
		return "", ErrCustomToolConfig
	}
	endpoint, err := url.Parse(strings.TrimSpace(base))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Opaque != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.RawPath != "" || strings.ContainsAny(endpoint.Host, "%") || (endpoint.Port() != "" && endpoint.Port() != "443") {
		return "", ErrCustomToolConfig
	}
	prefix := strings.TrimSuffix(endpoint.Path, "/")
	if prefix != "" && !isCustomToolPath(prefix) {
		return "", ErrCustomToolConfig
	}
	host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
	if (net.ParseIP(host) == nil && !strings.Contains(host, ".")) || customToolProtection.ValidateNetworkTarget(host, 443) != nil {
		return "", ErrCustomToolConfig
	}
	endpoint.Path = prefix + operation.Path
	return endpoint.String(), nil
}

func isCustomToolPath(value string) bool {
	return len(value) <= 512 && customToolPath.MatchString(value) && path.Clean(value) == value
}

func customToolBinding(id int, contract Contract) string {
	return fmt.Sprintf("advanced-custom-tool/v1/%d/%t", id, contract.IsSearch())
}

func customToolCipher() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("advanced-custom-tool/credential/v1\x00" + common.CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func SealCustomToolCredential(id int, contract Contract, credential string) (string, error) {
	if len(credential) == 0 || len(credential) > 4096 || strings.IndexFunc(credential, func(value rune) bool { return value <= ' ' || value >= 127 }) >= 0 {
		return "", ErrCustomToolConfig
	}
	box, err := customToolCipher()
	if err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(box.Seal(nil, nil, []byte(credential), []byte(customToolBinding(id, contract)))), nil
}

func (operation *CustomToolOperation) OpenCredential(id int, contract Contract) (string, error) {
	if operation == nil || len(operation.Credential) > 6000 {
		return "", ErrCustomToolConfig
	}
	sealed, err := base64.RawStdEncoding.DecodeString(operation.Credential)
	if err != nil {
		return "", ErrCustomToolConfig
	}
	box, err := customToolCipher()
	if err != nil {
		return "", err
	}
	plaintext, err := box.Open(nil, nil, sealed, []byte(customToolBinding(id, contract)))
	if err != nil || len(plaintext) == 0 {
		return "", ErrCustomToolConfig
	}
	return string(plaintext), nil
}

func (operation *CustomToolOperation) ProofFor(id int, contract Contract, base string) string {
	if _, err := operation.Endpoint(base); err != nil || operation.Credential == "" {
		return ""
	}
	// The sealed credential includes a random nonce, so re-saving the same key also revokes the proof.
	return common.GenerateHMAC(customToolBinding(id, contract) + "\x00" + base + "\x00" + operation.Protocol + "\x00" + operation.Path + "\x00" + operation.Credential)
}

func (operation *CustomToolOperation) Verified(id int, contract Contract, base string) bool {
	proof := operation.ProofFor(id, contract, base)
	return proof != "" && hmac.Equal([]byte(operation.Proof), []byte(proof))
}

func (channel ChannelContext) CustomOperation(contract Contract) *CustomToolOperation {
	if channel.ChannelType != constant.ChannelTypeAdvancedCustom || channel.ChannelID <= 0 {
		return nil
	}
	tools, err := DecodeCustomTools(channel.CustomTools)
	if err != nil {
		return nil
	}
	if selected := tools.Operation(contract); selected != nil {
		return *selected
	}
	return nil
}
