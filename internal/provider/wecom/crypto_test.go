package wecom

import (
	"encoding/base64"
	"encoding/xml"
	"testing"
)

func TestPKCS7PadUnpad(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		blockSize int
	}{
		{"empty", []byte{}, 16},
		{"one byte", []byte{0x01}, 16},
		{"exact block", make([]byte, 16), 16},
		{"block+1", make([]byte, 17), 16},
		{"large", make([]byte, 255), 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			padded := pkcs7Pad(tt.data, tt.blockSize)

			if len(padded)%tt.blockSize != 0 {
				t.Fatalf("padded length %d not multiple of %d", len(padded), tt.blockSize)
			}

			unpadded, err := pkcs7Unpad(padded)
			if err != nil {
				t.Fatalf("unpad error: %v", err)
			}

			if len(unpadded) != len(tt.data) {
				t.Fatalf("unpadded length %d != original %d", len(unpadded), len(tt.data))
			}

			for i := range tt.data {
				if unpadded[i] != tt.data[i] {
					t.Fatalf("byte %d: got %d, want %d", i, unpadded[i], tt.data[i])
				}
			}
		})
	}
}

func TestPKCS7UnpadInvalid(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"zero padding", []byte{0x00}},
		{"padding too large", []byte{0x10, 0x10}}, // padding=16 but only 2 bytes
		{"inconsistent padding", []byte{0x03, 0x03, 0x02}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pkcs7Unpad(tt.data)
			if err == nil {
				t.Fatal("expected error for invalid padding")
			}
		})
	}
}

func TestCallbackCrypto_NewWithInvalidKey(t *testing.T) {
	_, err := NewCallbackCrypto("token", "short", "corpid")
	if err == nil {
		t.Fatal("expected error for short AES key")
	}
}

func TestCallbackCrypto_NewWithValidKey(t *testing.T) {
	// Generate a valid 43-char base64 key (32 bytes)
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	// Remove trailing "=" — WeCom keys are 43 chars without padding
	key = key[:43]

	c, err := NewCallbackCrypto("testtoken", key, "testcorp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c.corpID != "testcorp" {
		t.Fatalf("corpID mismatch: %s", c.corpID)
	}

	if len(c.aesKey) != 32 {
		t.Fatalf("aesKey length: %d, expected 32", len(c.aesKey))
	}
}

func TestCallbackCrypto_VerifyURLSignature(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	key = key[:43]

	c, err := NewCallbackCrypto("testtoken", key, "testcorp")
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	// Signature should be consistent for same inputs
	sig1 := c.VerifyURLSignature("", "12345", "nonce1")
	sig2 := c.VerifyURLSignature("", "12345", "nonce1")

	if sig1 != sig2 {
		t.Fatal("signature verification inconsistent")
	}
}

func TestCallbackCrypto_EncryptDecryptRoundTrip(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	key = key[:43]

	c, err := NewCallbackCrypto("testtoken", key, "testcorp")
	if err != nil {
		t.Fatalf("create crypto: %v", err)
	}

	originalMsg := "<xml><Content>Hello WeChat</Content></xml>"
	timestamp := "1234567890"
	nonce := "testnonce"

	// Encrypt
	encrypted, signature, err := c.EncryptMessage(originalMsg, timestamp, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if encrypted == "" {
		t.Fatal("encrypted message is empty")
	}
	if signature == "" {
		t.Fatal("signature is empty")
	}

	// Verify signature
	if !c.VerifySignature(signature, timestamp, nonce, encrypted) {
		t.Fatal("signature verification failed")
	}

	// Decrypt
	decrypted, receiveID, err := c.DecryptMessage(encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if string(decrypted) != originalMsg {
		t.Fatalf("decrypted message mismatch:\ngot:  %s\nwant: %s", decrypted, originalMsg)
	}

	if receiveID != "testcorp" {
		t.Fatalf("receiveID mismatch: %s", receiveID)
	}
}

func TestCallbackCrypto_DecryptWithWrongCorpID(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	key = key[:43]

	c1, _ := NewCallbackCrypto("token", key, "corp1")
	c2, _ := NewCallbackCrypto("token", key, "corp2")

	encrypted, _, err := c1.EncryptMessage("test", "123", "nonce")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Decrypting with different corpID should fail
	_, _, err = c2.DecryptMessage(encrypted)
	if err == nil {
		t.Fatal("expected error for wrong corp ID")
	}
}

func TestCallbackEncryptedXML_Parse(t *testing.T) {
	xmlData := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<Encrypt><![CDATA[encrypted_content_here]]></Encrypt>
		<AgentID><![CDATA[1000001]]></AgentID>
	</xml>`

	var result CallbackEncryptedXML
	err := xml.Unmarshal([]byte(xmlData), &result)
	if err != nil {
		t.Fatalf("parse XML: %v", err)
	}

	if result.ToUserName != "testcorp" {
		t.Fatalf("ToUserName: %s", result.ToUserName)
	}
	if result.Encrypt != "encrypted_content_here" {
		t.Fatalf("Encrypt: %s", result.Encrypt)
	}
	if result.AgentID != "1000001" {
		t.Fatalf("AgentID: %s", result.AgentID)
	}
}

func TestCallbackMessage_ParseText(t *testing.T) {
	xmlData := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user001]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[text]]></MsgType>
		<Content><![CDATA[Hello from WeChat]]></Content>
		<MsgId>1234567890</MsgId>
		<AgentID>1000001</AgentID>
	</xml>`

	var msg CallbackMessage
	err := xml.Unmarshal([]byte(xmlData), &msg)
	if err != nil {
		t.Fatalf("parse XML: %v", err)
	}

	if msg.FromUserName != "user001" {
		t.Fatalf("FromUserName: %s", msg.FromUserName)
	}
	if msg.MsgType != "text" {
		t.Fatalf("MsgType: %s", msg.MsgType)
	}
	if msg.Content != "Hello from WeChat" {
		t.Fatalf("Content: %s", msg.Content)
	}
	if msg.MsgID != 1234567890 {
		t.Fatalf("MsgID: %d", msg.MsgID)
	}
}

func TestCallbackMessage_ParseImage(t *testing.T) {
	xmlData := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user001]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[image]]></MsgType>
		<PicUrl><![CDATA[https://example.com/image.jpg]]></PicUrl>
		<MediaId><![CDATA[media_id_001]]></MediaId>
		<MsgId>1234567891</MsgId>
	</xml>`

	var msg CallbackMessage
	err := xml.Unmarshal([]byte(xmlData), &msg)
	if err != nil {
		t.Fatalf("parse XML: %v", err)
	}

	if msg.MsgType != "image" {
		t.Fatalf("MsgType: %s", msg.MsgType)
	}
	if msg.PicURL != "https://example.com/image.jpg" {
		t.Fatalf("PicURL: %s", msg.PicURL)
	}
	if msg.MediaID != "media_id_001" {
		t.Fatalf("MediaID: %s", msg.MediaID)
	}
}

func TestCallbackMessage_ParseEvent(t *testing.T) {
	xmlData := `<xml>
		<ToUserName><![CDATA[testcorp]]></ToUserName>
		<FromUserName><![CDATA[user001]]></FromUserName>
		<CreateTime>1348831860</CreateTime>
		<MsgType><![CDATA[event]]></MsgType>
		<Event><![CDATA[change_external_contact]]></Event>
		<ChangeType><![CDATA[add_external_contact]]></ChangeType>
		<UserID><![CDATA[internal_user]]></UserID>
		<ExternalUserID><![CDATA[ext_user_001]]></ExternalUserID>
	</xml>`

	var msg CallbackMessage
	err := xml.Unmarshal([]byte(xmlData), &msg)
	if err != nil {
		t.Fatalf("parse XML: %v", err)
	}

	if msg.MsgType != "event" {
		t.Fatalf("MsgType: %s", msg.MsgType)
	}
	if msg.Event != "change_external_contact" {
		t.Fatalf("Event: %s", msg.Event)
	}
	if msg.ChangeType != "add_external_contact" {
		t.Fatalf("ChangeType: %s", msg.ChangeType)
	}
	if msg.ExternalUserID != "ext_user_001" {
		t.Fatalf("ExternalUserID: %s", msg.ExternalUserID)
	}
}
