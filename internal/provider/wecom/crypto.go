package wecom

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// CallbackCrypto handles WeCom callback message encryption and decryption.
// WeCom uses AES-256-CBC with PKCS#7 padding.
type CallbackCrypto struct {
	token  string
	aesKey []byte
	corpID string
}

// NewCallbackCrypto creates a new callback crypto handler.
// encodingAESKey is the Base64-encoded 43-char key from WeCom console.
func NewCallbackCrypto(token, encodingAESKey, corpID string) (*CallbackCrypto, error) {
	// WeCom's EncodingAESKey is base64 encoded, 43 chars -> 32 bytes
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("decode aes key: %w", err)
	}

	if len(aesKey) != 32 {
		return nil, fmt.Errorf("invalid aes key length: got %d, expected 32", len(aesKey))
	}

	return &CallbackCrypto{
		token:  token,
		aesKey: aesKey,
		corpID: corpID,
	}, nil
}

// VerifySignature verifies the callback URL signature using constant-time comparison.
// signature = SHA1(sort(token, timestamp, nonce, encrypt_msg))
func (c *CallbackCrypto) VerifySignature(signature, timestamp, nonce, encryptMsg string) bool {
	items := []string{c.token, timestamp, nonce, encryptMsg}
	sort.Strings(items)

	hash := sha1.Sum([]byte(strings.Join(items, "")))
	expected := fmt.Sprintf("%x", hash)

	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// VerifyURLSignature verifies the URL verification callback using constant-time comparison.
// signature = SHA1(sort(token, timestamp, nonce))
func (c *CallbackCrypto) VerifyURLSignature(signature, timestamp, nonce string) bool {
	items := []string{c.token, timestamp, nonce}
	sort.Strings(items)

	hash := sha1.Sum([]byte(strings.Join(items, "")))
	expected := fmt.Sprintf("%x", hash)

	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

// DecryptMessage decrypts an encrypted callback message.
// Returns the raw XML message content and the receiving corp ID.
func (c *CallbackCrypto) DecryptMessage(encryptedBase64 string) ([]byte, string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		return nil, "", fmt.Errorf("base64 decode: %w", err)
	}

	// AES-256-CBC decrypt
	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		return nil, "", fmt.Errorf("create cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize || len(ciphertext)%aes.BlockSize != 0 {
		return nil, "", fmt.Errorf("invalid ciphertext length")
	}

	iv := c.aesKey[:aes.BlockSize]
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS#7 padding
	plaintext, err = pkcs7Unpad(plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("pkcs7 unpad: %w", err)
	}

	// Parse decrypted content:
	// 16 bytes random + 4 bytes msg length (big endian) + msg + receiveid
	if len(plaintext) < 20 {
		return nil, "", fmt.Errorf("decrypted data too short")
	}

	// Skip 16 bytes random prefix
	msgLenBytes := plaintext[16:20]
	msgLen := int(binary.BigEndian.Uint32(msgLenBytes))

	if 20+msgLen > len(plaintext) {
		return nil, "", fmt.Errorf("invalid message length: %d", msgLen)
	}

	msgContent := plaintext[20 : 20+msgLen]
	receiveID := string(plaintext[20+msgLen:])

	// Verify the receiving corp ID
	if subtle.ConstantTimeCompare([]byte(receiveID), []byte(c.corpID)) != 1 {
		return nil, "", fmt.Errorf("corp id mismatch")
	}

	return msgContent, receiveID, nil
}

// EncryptMessage encrypts a response message for WeCom.
func (c *CallbackCrypto) EncryptMessage(msg, timestamp, nonce string) (string, string, error) {
	// Build plaintext: 16 random bytes + 4 bytes msg length + msg + corp_id
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", fmt.Errorf("generate random bytes: %w", err)
	}

	msgBytes := []byte(msg)
	msgLenBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(msgLenBytes, uint32(len(msgBytes)))

	plaintext := bytes.Join([][]byte{
		randomBytes,
		msgLenBytes,
		msgBytes,
		[]byte(c.corpID),
	}, nil)

	// PKCS#7 padding
	plaintext = pkcs7Pad(plaintext, aes.BlockSize)

	// AES-256-CBC encrypt
	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		return "", "", fmt.Errorf("create cipher: %w", err)
	}

	iv := c.aesKey[:aes.BlockSize]
	ciphertext := make([]byte, len(plaintext))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, plaintext)

	encryptedBase64 := base64.StdEncoding.EncodeToString(ciphertext)

	// Generate signature
	items := []string{c.token, timestamp, nonce, encryptedBase64}
	sort.Strings(items)
	hash := sha1.Sum([]byte(strings.Join(items, "")))
	signature := fmt.Sprintf("%x", hash)

	return encryptedBase64, signature, nil
}

// pkcs7Pad pads data to a multiple of blockSize using PKCS#7.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

// pkcs7Unpad removes PKCS#7 padding.
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padding := int(data[len(data)-1])
	if padding > len(data) || padding == 0 {
		return nil, fmt.Errorf("invalid padding: %d", padding)
	}
	for i := len(data) - padding; i < len(data); i++ {
		if data[i] != byte(padding) {
			return nil, fmt.Errorf("invalid padding byte at %d", i)
		}
	}
	return data[:len(data)-padding], nil
}

// --- XML structures for callback messages ---

// CallbackEncryptedXML is the outer XML wrapper for encrypted callback messages.
type CallbackEncryptedXML struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	Encrypt    string   `xml:"Encrypt"`
	AgentID    string   `xml:"AgentID"`
}

// CallbackMessage is the decrypted XML message from WeCom callback.
type CallbackMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content,omitempty"`
	MsgID        int64    `xml:"MsgId,omitempty"`
	AgentID      int      `xml:"AgentID,omitempty"`

	// Image
	PicURL  string `xml:"PicUrl,omitempty"`
	MediaID string `xml:"MediaId,omitempty"`

	// Voice
	Format      string `xml:"Format,omitempty"`
	Recognition string `xml:"Recognition,omitempty"`

	// Video / ShortVideo
	ThumbMediaID string `xml:"ThumbMediaId,omitempty"`

	// Location
	LocationX float64 `xml:"Location_X,omitempty"`
	LocationY float64 `xml:"Location_Y,omitempty"`
	Scale     int     `xml:"Scale,omitempty"`
	Label     string  `xml:"Label,omitempty"`

	// Link
	Title       string `xml:"Title,omitempty"`
	Description string `xml:"Description,omitempty"`
	URL         string `xml:"Url,omitempty"`

	// Event
	Event    string `xml:"Event,omitempty"`
	EventKey string `xml:"EventKey,omitempty"`

	// External contact events
	ChangeType     string `xml:"ChangeType,omitempty"`
	UserID         string `xml:"UserID,omitempty"`
	ExternalUserID string `xml:"ExternalUserID,omitempty"`
	WelcomeCode    string `xml:"WelcomeCode,omitempty"`

	// Group chat events
	ChatID     string `xml:"ChatId,omitempty"`
	UpdateDetail string `xml:"UpdateDetail,omitempty"`
}

// EncryptedResponseXML wraps an encrypted response to WeCom.
type EncryptedResponseXML struct {
	XMLName      xml.Name `xml:"xml"`
	Encrypt      string   `xml:"Encrypt"`
	MsgSignature string   `xml:"MsgSignature"`
	TimeStamp    string   `xml:"TimeStamp"`
	Nonce        string   `xml:"Nonce"`
}
