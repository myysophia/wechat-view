package chatlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	// Optional: custom HTTP client (timeouts)
	HTTP *http.Client
}

type Message struct {
	ID         string                 `json:"id,omitempty"`
	MsgID      string                 `json:"msgId,omitempty"`
	Talker     string                 `json:"talker,omitempty"`
	TalkerName string                 `json:"talkerName,omitempty"`
	Sender     string                 `json:"sender,omitempty"`
	SenderName string                 `json:"senderName,omitempty"`
	From       string                 `json:"from,omitempty"`
	Nickname   string                 `json:"nickname,omitempty"`
	Timestamp  int64                  `json:"timestamp,omitempty"`
	CreateTime int64                  `json:"createTime,omitempty"`
	Time       string                 `json:"time,omitempty"`
	Content    string                 `json:"content,omitempty"`
	Text       string                 `json:"text,omitempty"`
	Type       string                 `json:"type,omitempty"`
	MsgType    int                    `json:"msgType,omitempty"`
	SubType    int                    `json:"subType,omitempty"`
	IsChatRoom bool                   `json:"isChatRoom,omitempty"`
	IsSelf     bool                   `json:"isSelf,omitempty"`
	MediaMD5   string                 `json:"mediaMD5,omitempty"`
	MediaPath  string                 `json:"mediaPath,omitempty"`
	Mentions   []string               `json:"mentions,omitempty"`
	Emojis     []string               `json:"emojis,omitempty"`
	Reference  *Reference             `json:"reference,omitempty"`
	IsQuestion bool                   `json:"isQuestion,omitempty"`
	Share      *Share                 `json:"share,omitempty"`
	Extras     map[string]interface{} `json:"-"`
}

type Reference struct {
	Seq        int64  `json:"seq,omitempty"`
	Time       string `json:"time,omitempty"`
	Talker     string `json:"talker,omitempty"`
	TalkerName string `json:"talkerName,omitempty"`
	Sender     string `json:"sender,omitempty"`
	SenderName string `json:"senderName,omitempty"`
	Type       int    `json:"type,omitempty"`
	SubType    int    `json:"subType,omitempty"`
	Content    string `json:"content,omitempty"`
}

type Share struct {
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
	URL   string `json:"url,omitempty"`
}

// FetchDay calls chatlog local API for one day and returns best-effort parsed messages.
func (c Client) FetchDay(day, talker, keyword string) ([]Message, map[string]any, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	u, _ := url.Parse(base + "/api/v1/chatlog")
	q := u.Query()
	q.Set("time", day)
	q.Set("talker", talker)
	if keyword != "" {
		q.Set("keyword", keyword)
	}
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	req, _ := http.NewRequest(http.MethodGet, u.String(), nil)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(b))
	}

	var raw any
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, nil, err
	}

	// Try to locate array of messages under common keys
	arr, meta := normalizeResponse(raw)
	if arr == nil {
		return nil, nil, errors.New("unable to locate messages array in response")
	}

	msgs := make([]Message, 0, len(arr))
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			msgs = append(msgs, mapToMessage(m))
		}
	}
	return msgs, meta, nil
}

// normalizeResponse tries common envelopes: {data: []}, {list: []}, {messages: []}, or root []. Returns messages array and meta.
func normalizeResponse(v any) ([]any, map[string]any) {
	switch x := v.(type) {
	case []any:
		return x, nil
	case map[string]any:
		// potential meta is the whole object minus the array
		keys := []string{"data", "list", "messages", "items", "result"}
		for _, k := range keys {
			if arr, ok := x[k].([]any); ok {
				meta := make(map[string]any)
				for mk, mv := range x {
					if mk == k {
						continue
					}
					meta[mk] = mv
				}
				return arr, meta
			}
		}
	}
	return nil, nil
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case json.Number:
		i, _ := t.Int64()
		return i
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	}
	return 0
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func mapToMessage(m map[string]any) Message {
	msg := Message{
		ID:         toString(firstNonEmpty(m["id"], m["_id"], m["msgId"], m["msgID"])),
		MsgID:      toString(firstNonEmpty(m["msgId"], m["msgID"], m["id"])),
		Talker:     toString(firstNonEmpty(m["talker"], m["chatroom"], m["room"], m["toUserName"])),
		TalkerName: toString(firstNonEmpty(m["talkerName"], m["roomName"])),
		Sender:     toString(firstNonEmpty(m["sender"], m["from"], m["fromUser"], m["fromUserName"])),
		SenderName: toString(firstNonEmpty(m["senderName"], m["displayName"], m["nickname"], m["senderNick"])),
		From:       toString(m["from"]),
		Nickname:   toString(firstNonEmpty(m["nickname"], m["displayName"], m["senderName"])),
		Timestamp:  toInt64(firstNonEmpty(m["seq"], m["timestamp"], m["ts"], m["createTime"])),
		CreateTime: toInt64(m["createTime"]),
		Time:       toString(firstNonEmpty(m["time"], m["createdAt"], m["date"])),
		Content:    toString(firstNonEmpty(m["content"], m["text"], m["message"], m["body"])),
		Text:       toString(m["text"]),
		Type:       toString(firstNonEmpty(m["type"], m["msgTypeName"])),
		MsgType:    int(toInt64(firstNonEmpty(m["msgType"], m["type"]))),
		SubType:    int(toInt64(m["subType"])),
		Extras:     map[string]any{},
	}
	// booleans
	if v, ok := m["isChatRoom"]; ok {
		switch t := v.(type) {
		case bool:
			msg.IsChatRoom = t
		case string:
			msg.IsChatRoom = t == "true" || t == "1"
		case json.Number:
			i, _ := t.Int64()
			msg.IsChatRoom = i != 0
		case float64:
			msg.IsChatRoom = t != 0
		}
	}
	if v, ok := m["isSelf"]; ok {
		switch t := v.(type) {
		case bool:
			msg.IsSelf = t
		case string:
			msg.IsSelf = t == "true" || t == "1"
		case json.Number:
			i, _ := t.Int64()
			msg.IsSelf = i != 0
		case float64:
			msg.IsSelf = t != 0
		}
	}
	text := msg.Content
	if text == "" {
		text = msg.Text
	}
	if text != "" {
		msg.Mentions = extractMentions(text)
		msg.Emojis = extractBracketEmojis(text)
		msg.IsQuestion = isQuestionText(text)
	}

	// contents for media / references
	if c, ok := m["contents"].(map[string]any); ok {
		if v, ok2 := c["md5"]; ok2 {
			msg.MediaMD5 = toString(v)
		}
		if v, ok2 := c["path"]; ok2 {
			msg.MediaPath = toString(v)
		}
		if refRaw, ok2 := c["refer"].(map[string]any); ok2 {
			if ref := parseReference(refRaw); ref != nil {
				msg.Reference = ref
			}
		}
		if msg.MsgType == 49 {
			if title := toString(c["title"]); title != "" || toString(c["url"]) != "" {
				msg.Share = &Share{
					Title: toString(c["title"]),
					Desc:  toString(c["desc"]),
					URL:   toString(c["url"]),
				}
			} else if ext, ok := msg.Extras["appMsg"].(map[string]any); ok {
				// fallback if share metadata stored elsewhere
				msg.Share = &Share{
					Title: toString(ext["title"]),
					Desc:  toString(ext["desc"]),
					URL:   toString(ext["url"]),
				}
			}
		}
	}
	// copy remaining unknowns into Extras
	appMsg := map[string]any{}
	for k, v := range m {
		switch k {
		case "id", "_id", "msgId", "msgID", "talker", "chatroom", "room", "toUserName", "talkerName", "sender", "from", "fromUser", "fromUserName", "senderName", "senderNick", "nickname", "displayName", "seq", "timestamp", "ts", "createTime", "time", "createdAt", "date", "content", "text", "message", "body", "type", "msgTypeName", "msgType", "subType", "isChatRoom", "isSelf", "contents":
			// skip
		case "appMsgInfo", "appMsg":
			if mv, ok := v.(map[string]any); ok {
				for kk, vv := range mv {
					appMsg[kk] = vv
				}
			}
		default:
			msg.Extras[k] = v
		}
	}
	if msg.Share == nil && len(appMsg) > 0 {
		msg.Share = &Share{
			Title: toString(appMsg["title"]),
			Desc:  toString(appMsg["desc"]),
			URL:   toString(appMsg["url"]),
		}
	}
	return msg
}

var (
	mentionRegexp      = regexp.MustCompile(`@([^\s@]{1,32})`)
	bracketEmojiRegexp = regexp.MustCompile(`\[(.+?)\]`)
	spaceReplacer      = strings.NewReplacer(
		"\u00a0", " ",
		"\u2002", " ",
		"\u2003", " ",
		"\u2005", " ",
		"\u2009", " ",
		"\u200a", " ",
		"\u200b", "",
	)
)

func parseReference(m map[string]any) *Reference {
	if len(m) == 0 {
		return nil
	}
	ref := &Reference{
		Seq:        toInt64(m["seq"]),
		Time:       toString(m["time"]),
		Talker:     toString(m["talker"]),
		TalkerName: toString(m["talkerName"]),
		Sender:     toString(m["sender"]),
		SenderName: toString(m["senderName"]),
		Type:       int(toInt64(firstNonEmpty(m["type"], m["msgType"]))),
		SubType:    int(toInt64(m["subType"])),
		Content:    toString(firstNonEmpty(m["content"], m["text"], m["message"])),
	}
	if ref.Seq == 0 && ref.Time == "" && ref.Sender == "" && ref.SenderName == "" && ref.Content == "" {
		return nil
	}
	return ref
}

func extractMentions(s string) []string {
	if s == "" {
		return nil
	}
	cleaned := normalizeSpaces(s)
	matches := mentionRegexp.FindAllStringSubmatch(cleaned, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		name := strings.Trim(m[1], "，。,.;!?！？：:•· ")
		name = strings.Trim(name, "\u2005\u2002\u2003\u2009\u200a\u200b")
		if name != "" {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractBracketEmojis(s string) []string {
	if s == "" {
		return nil
	}
	matches := bracketEmojiRegexp.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		token := strings.TrimSpace(m[1])
		if token != "" {
			out = append(out, token)
		}
	}
	return out
}

func isQuestionText(s string) bool {
	if s == "" {
		return false
	}
	cleaned := strings.TrimSpace(normalizeSpaces(s))
	if cleaned == "" {
		return false
	}
	if strings.ContainsAny(cleaned, "？?") {
		return true
	}
	lower := strings.ToLower(cleaned)
	keywords := []string{"请问", "如何", "怎么", "是否", "能否", "可以吗", "有没有", "能不能", "麻烦", "help", "any idea", "why"}
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func normalizeSpaces(s string) string {
	if s == "" {
		return s
	}
	return spaceReplacer.Replace(s)
}

func firstNonEmpty(vals ...any) any {
	for _, v := range vals {
		switch t := v.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(t) != "" {
				return v
			}
		default:
			return v
		}
	}
	return nil
}
