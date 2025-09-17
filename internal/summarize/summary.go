package summarize

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"wechat-view/internal/chatlog"
)

type Summary struct {
	TotalMessages   int        `json:"totalMessages"`
	UniqueSenders   int        `json:"uniqueSenders"`
	TopSenders      []KV       `json:"topSenders"`
	TopLinks        []string   `json:"topLinks"`
	HourlyHistogram [24]int    `json:"hourlyHistogram"`
	Keywords        []KV       `json:"keywords"`
	PeakHour        int        `json:"peakHour"`
	Highlights      []string   `json:"highlights"`
	Topics          []Topic    `json:"topics"`
	ImageCount      int        `json:"imageCount"`
	GroupVibes      GroupVibes `json:"groupVibes"`
	ReplyDebt       ReplyDebt  `json:"replyDebt"`
}

type Topic struct {
	Name           string   `json:"name"`
	Keywords       []string `json:"keywords"`
	Count          int      `json:"count"`
	Representative string   `json:"representative"`
}

type GroupVibes struct {
	Score       int      `json:"score"`
	Activity    float64  `json:"activity"`
	Sentiment   float64  `json:"sentiment"`
	InfoDensity float64  `json:"infoDensity"`
	Controversy float64  `json:"controversy"`
	Tone        string   `json:"tone"`
	Reasons     []string `json:"reasons"`
}

type ReplyDebt struct {
	Outstanding        []ReplyItem `json:"outstanding"`
	Resolved           []ReplyItem `json:"resolved"`
	AvgResponseMinutes float64     `json:"avgResponseMinutes"`
	BestResponseHours  []int       `json:"bestResponseHours"`
}

type ReplyItem struct {
	Questioner      string   `json:"questioner"`
	Question        string   `json:"question"`
	AskedAt         string   `json:"askedAt"`
	Mentions        []string `json:"mentions,omitempty"`
	AgeMinutes      float64  `json:"ageMinutes,omitempty"`
	ResponseMinutes float64  `json:"responseMinutes,omitempty"`
	Responders      []string `json:"responders,omitempty"`
}

type vibeTracker struct {
	infoDense    int
	mentionMsg   int
	questionMsg  int
	exclaimMsg   int
	sentimentPos float64
	sentimentNeg float64
}

type questionStatus struct {
	Index                int
	Message              chatlog.Message
	AskedAt              time.Time
	Mentions             []string
	NormalizedQuestioner string
	Resolved             bool
	ResponseMinutes      float64
	ResponseHour         int
	Responders           map[string]string
}

type KV struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

func BuildSummary(msgs []chatlog.Message) Summary {
	sum := Summary{}
	sum.TotalMessages = len(msgs)

	senderCount := map[string]int{}
	linkCount := map[string]int{}
	tokenCount := map[string]int{}

	messagesText := make([]string, 0, len(msgs))
	analytics := vibeTracker{}
	questions := make([]*questionStatus, 0)
	lastTime := time.Time{}

	for idx, orig := range msgs {
		m := orig
		s := senderDisplay(m)
		if s != "" {
			senderCount[s]++
		}

		// hour histogram from Timestamp/CreateTime
		ts := m.Timestamp
		if ts == 0 {
			ts = m.CreateTime
		}
		if ts > 0 { // assume seconds if < 10^12 else ms
			if ts > 1_000_000_000_000 { // ms
				ts = ts / 1000
			}
			h := time.Unix(ts, 0).Local().Hour()
			sum.HourlyHistogram[h]++
		}

		// text, links, media count
		text := m.Content
		if text == "" {
			text = m.Text
		}
		if text != "" {
			messagesText = append(messagesText, text)
		}
		foundLinks := extractURLs(text)
		if m.Share != nil && m.Share.URL != "" {
			foundLinks = append(foundLinks, m.Share.URL)
		}
		for _, u := range foundLinks {
			linkCount[u]++
		}
		if m.MsgType == 3 { // image
			sum.ImageCount++
		}
		if len(foundLinks) > 0 || runeLen(text) > 80 || m.MsgType == 49 {
			analytics.infoDense++
		}
		if len(m.Mentions) > 0 {
			analytics.mentionMsg++
		}
		if m.IsQuestion {
			analytics.questionMsg++
		}
		if strings.ContainsAny(text, "!！") {
			analytics.exclaimMsg++
		}
		pos, neg := sentimentSignals(text, m.Emojis)
		analytics.sentimentPos += pos
		analytics.sentimentNeg += neg

		msgTime := messageTime(m)
		if !msgTime.IsZero() && msgTime.After(lastTime) {
			lastTime = msgTime
		}

		for _, q := range questions {
			if q.Resolved {
				continue
			}
			if idx <= q.Index {
				continue
			}
			if msgTime.IsZero() || (!q.AskedAt.IsZero() && msgTime.Before(q.AskedAt)) {
				continue
			}
			if matchesQuestionResponse(m, q, text) {
				q.Resolved = true
				if !msgTime.IsZero() && !q.AskedAt.IsZero() && msgTime.After(q.AskedAt) {
					q.ResponseMinutes = msgTime.Sub(q.AskedAt).Minutes()
				}
				if msgTime.IsZero() {
					q.ResponseHour = -1
				} else {
					q.ResponseHour = msgTime.Hour()
				}
				if q.Responders == nil {
					q.Responders = make(map[string]string)
				}
				if display := senderDisplay(m); display != "" {
					q.Responders[normalizeName(display)] = display
				}
			}
		}

		if shouldTrackQuestion(m, text) {
			qMsg := m
			questions = append(questions, &questionStatus{
				Index:                idx,
				Message:              qMsg,
				AskedAt:              msgTime,
				Mentions:             uniqueStrings(m.Mentions),
				NormalizedQuestioner: normalizeName(senderDisplay(m)),
			})
		}

		// tokenization (ASCII + simple Chinese grams)
		for _, tok := range asciiTokens(text) {
			tok = strings.ToLower(tok)
			if stopwordEN[tok] || len(tok) <= 2 {
				continue
			}
			tokenCount[tok]++
		}
		for _, tok := range chineseGrams(text) {
			if stopwordCN[tok] {
				continue
			}
			tokenCount[tok]++
		}
	}

	// derive peak hour
	peakHour := 0
	peakValue := 0
	for i, v := range sum.HourlyHistogram {
		if v > peakValue {
			peakValue = v
			peakHour = i
		}
	}
	sum.PeakHour = peakHour

	sum.UniqueSenders = len(senderCount)
	sum.TopSenders = topK(senderCount, 5)
	sum.TopLinks = topKKeys(linkCount, 5)
	sum.Keywords = topK(tokenCount, 20)

	// Build topics by top tokens; group messages containing that token
	topTokens := make([]string, 0, len(sum.Keywords))
	for _, kv := range sum.Keywords {
		topTokens = append(topTokens, kv.Key)
	}
	topics := make([]Topic, 0, 5)
	used := map[string]bool{}
	texts := messagesText
	for _, tk := range topTokens {
		if len(topics) >= 5 {
			break
		}
		// collect messages containing tk
		idxs := make([]int, 0)
		for i, t := range texts {
			if strings.Contains(t, tk) {
				idxs = append(idxs, i)
			}
		}
		if len(idxs) < 3 { // too weak
			continue
		}
		// avoid synonyms/overlap: skip if similar topic name already used (prefix/contain)
		skip := false
		for k := range used {
			if strings.Contains(k, tk) || strings.Contains(tk, k) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// representative: longest message among idxs (trim to 120 chars for display later)
		rep := ""
		long := 0
		for _, i := range idxs {
			if l := runeLen(texts[i]); l > long {
				long = l
				rep = texts[i]
			}
		}
		topics = append(topics, Topic{
			Name:           tk,
			Keywords:       []string{tk},
			Count:          len(idxs),
			Representative: rep,
		})
		used[tk] = true
	}
	sum.Topics = topics

	// Highlights (concise bullets)
	sum.Highlights = buildHighlights(sum)
	sum.GroupVibes = buildGroupVibes(sum, analytics)
	sum.ReplyDebt = buildReplyDebt(questions, lastTime)
	return sum
}

func buildHighlights(s Summary) []string {
	hi := []string{}
	hi = append(hi, sprintf("消息 %d 条，活跃 %d 人；峰值 %02d:00-%02d:59", s.TotalMessages, s.UniqueSenders, s.PeakHour, s.PeakHour))
	if len(s.TopSenders) > 0 {
		parts := []string{}
		for i := 0; i < len(s.TopSenders) && i < 3; i++ {
			kv := s.TopSenders[i]
			parts = append(parts, sprintf("%s(%d)", kv.Key, kv.Count))
		}
		hi = append(hi, "Top 发送者："+strings.Join(parts, "、"))
	}
	if len(s.Topics) > 0 {
		names := []string{}
		for i := 0; i < len(s.Topics) && i < 3; i++ {
			names = append(names, s.Topics[i].Name)
		}
		hi = append(hi, "热门主题："+strings.Join(names, "、"))
	}
	if len(s.TopLinks) > 0 {
		// show first domain
		u := s.TopLinks[0]
		if uu, err := url.Parse(u); err == nil && uu.Host != "" {
			hi = append(hi, sprintf("热门链接 %d 个，例如 %s", len(s.TopLinks), uu.Host))
		} else {
			hi = append(hi, sprintf("热门链接 %d 个", len(s.TopLinks)))
		}
	}
	if s.ImageCount > 0 {
		hi = append(hi, sprintf("图片 %d 张", s.ImageCount))
	}
	return hi
}

func buildGroupVibes(sum Summary, analytics vibeTracker) GroupVibes {
	if sum.TotalMessages == 0 {
		return GroupVibes{}
	}
	total := float64(sum.TotalMessages)
	activity := clamp01(float64(sum.TotalMessages)/80.0 + float64(sum.UniqueSenders)/25.0)
	sentiment := clamp01(0.5 + (analytics.sentimentPos-analytics.sentimentNeg)/(total*1.5))
	infoDensity := clamp01(float64(analytics.infoDense) / total)
	controversy := clamp01(float64(analytics.questionMsg+analytics.mentionMsg+analytics.exclaimMsg) / (total * 1.0))
	balanced := clamp01(1 - math.Abs(0.35-controversy)/0.35)
	score := int(math.Round((activity*0.35 + sentiment*0.3 + infoDensity*0.2 + balanced*0.15) * 100))
	tone := "讨论平稳"
	switch {
	case score >= 85:
		tone = "群氛高涨"
	case score >= 70:
		tone = "活跃良好"
	case score <= 40:
		tone = "氛围偏冷"
	}
	reasons := []string{}
	if activity >= 0.7 {
		reasons = append(reasons, sprintf("活跃度高（%d 条、%d 人参与）", sum.TotalMessages, sum.UniqueSenders))
	} else if activity <= 0.3 {
		reasons = append(reasons, "消息量偏低，讨论热度不足")
	}
	if sentiment >= 0.6 {
		reasons = append(reasons, "情绪偏正向，互动轻松")
	} else if sentiment <= 0.4 {
		reasons = append(reasons, "负面/吐槽内容偏多")
	}
	if infoDensity >= 0.5 {
		reasons = append(reasons, "信息密度高（链接或长文较多）")
	}
	if controversy >= 0.55 {
		reasons = append(reasons, "争议度高，需要关注共识")
	} else if controversy <= 0.2 {
		reasons = append(reasons, "讨论较温和，可适度引导观点碰撞")
	}
	return GroupVibes{
		Score:       score,
		Activity:    roundTo(activity, 2),
		Sentiment:   roundTo(sentiment, 2),
		InfoDensity: roundTo(infoDensity, 2),
		Controversy: roundTo(controversy, 2),
		Tone:        tone,
		Reasons:     reasons,
	}
}

func buildReplyDebt(questions []*questionStatus, lastTime time.Time) ReplyDebt {
	if len(questions) == 0 {
		return ReplyDebt{}
	}
	rd := ReplyDebt{}
	hourCounts := make(map[int]int)
	var totalResponse float64
	var responseCount float64
	for _, q := range questions {
		questioner := senderDisplay(q.Message)
		askedAtStr := ""
		if !q.AskedAt.IsZero() {
			askedAtStr = q.AskedAt.Format(time.RFC3339)
		}
		item := ReplyItem{
			Questioner: questioner,
			Question:   trimQuestionText(q.Message),
			AskedAt:    askedAtStr,
			Mentions:   q.Mentions,
		}
		if q.Resolved {
			item.ResponseMinutes = roundTo(q.ResponseMinutes, 1)
			if len(q.Responders) > 0 {
				responders := make([]string, 0, len(q.Responders))
				for _, name := range q.Responders {
					responders = append(responders, name)
				}
				sort.Strings(responders)
				item.Responders = responders
			}
			rd.Resolved = append(rd.Resolved, item)
			if q.ResponseMinutes > 0 {
				totalResponse += q.ResponseMinutes
				responseCount++
			}
			if q.ResponseHour >= 0 {
				hourCounts[q.ResponseHour]++
			}
		} else {
			if !lastTime.IsZero() && !q.AskedAt.IsZero() {
				age := lastTime.Sub(q.AskedAt).Minutes()
				if age < 0 {
					age = 0
				}
				item.AgeMinutes = roundTo(age, 1)
			}
			rd.Outstanding = append(rd.Outstanding, item)
		}
	}
	if responseCount > 0 {
		rd.AvgResponseMinutes = roundTo(totalResponse/responseCount, 1)
	}
	rd.BestResponseHours = bestHours(hourCounts, 3)
	return rd
}

func sentimentSignals(text string, emojis []string) (float64, float64) {
	if text == "" && len(emojis) == 0 {
		return 0, 0
	}
	lower := strings.ToLower(text)
	var pos, neg float64
	for _, token := range positiveLexicons {
		if token == "" {
			continue
		}
		if strings.Contains(text, token) || strings.Contains(lower, token) {
			pos += 1
			break
		}
	}
	for _, token := range negativeLexicons {
		if token == "" {
			continue
		}
		if strings.Contains(text, token) || strings.Contains(lower, token) {
			neg += 1
			break
		}
	}
	for _, e := range emojis {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if positiveEmojiSet[e] {
			pos += 0.5
		}
		if negativeEmojiSet[e] {
			neg += 0.5
		}
	}
	return pos, neg
}

func shouldTrackQuestion(m chatlog.Message, text string) bool {
	if !m.IsQuestion {
		return false
	}
	if m.MsgType != 0 && m.MsgType != 1 && m.MsgType != 49 {
		return false
	}
	if strings.TrimSpace(text) == "" {
		return false
	}
	if runeLen(text) < 2 {
		return false
	}
	return true
}

func matchesQuestionResponse(msg chatlog.Message, q *questionStatus, text string) bool {
	questioner := q.NormalizedQuestioner
	if questioner == "" {
		questioner = normalizeName(senderDisplay(q.Message))
	}
	if questioner == "" {
		return false
	}
	responderName := normalizeName(senderDisplay(msg))
	if responderName == "" || responderName == questioner {
		return false
	}
	if msg.Reference != nil {
		if normalizeName(msg.Reference.SenderName) == questioner {
			return true
		}
		refContent := strings.TrimSpace(msg.Reference.Content)
		questionContent := strings.TrimSpace(q.Message.Content)
		if refContent != "" && questionContent != "" {
			if strings.Contains(questionContent, refContent) || strings.Contains(refContent, questionContent) {
				return true
			}
		}
	}
	for _, mention := range msg.Mentions {
		if normalizeName(mention) == questioner {
			return true
		}
	}
	if len(q.Mentions) > 0 {
		for _, target := range q.Mentions {
			if normalizeName(target) == responderName {
				return true
			}
		}
	}
	return false
}

func messageTime(m chatlog.Message) time.Time {
	if m.Timestamp > 0 {
		ts := m.Timestamp
		if ts > 1_000_000_000_000 {
			ts = ts / 1000
		}
		return time.Unix(ts, 0).Local()
	}
	if m.CreateTime > 0 {
		ts := m.CreateTime
		if ts > 1_000_000_000_000 {
			ts = ts / 1000
		}
		return time.Unix(ts, 0).Local()
	}
	if m.Time != "" {
		if t, err := time.Parse(time.RFC3339, m.Time); err == nil {
			return t
		}
	}
	return time.Time{}
}

func trimQuestionText(m chatlog.Message) string {
	text := m.Content
	if text == "" {
		text = m.Text
	}
	text = strings.TrimSpace(text)
	if text == "" && m.Reference != nil {
		text = strings.TrimSpace(m.Reference.Content)
	}
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return text
}

func bestHours(counts map[int]int, limit int) []int {
	if len(counts) == 0 || limit == 0 {
		return nil
	}
	type kv struct {
		Hour  int
		Count int
	}
	arr := make([]kv, 0, len(counts))
	for h, c := range counts {
		if h < 0 {
			continue
		}
		arr = append(arr, kv{Hour: h, Count: c})
	}
	if len(arr) == 0 {
		return nil
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].Count == arr[j].Count {
			return arr[i].Hour < arr[j].Hour
		}
		return arr[i].Count > arr[j].Count
	})
	if limit > 0 && len(arr) > limit {
		arr = arr[:limit]
	}
	res := make([]int, 0, len(arr))
	for _, item := range arr {
		res = append(res, item.Hour)
	}
	return res
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func roundTo(v float64, digits int) float64 {
	if digits < 0 {
		digits = 0
	}
	factor := math.Pow(10, float64(digits))
	return math.Round(v*factor) / factor
}

func normalizeName(s string) string {
	if s == "" {
		return ""
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	clean := nameReplacer.Replace(lower)
	var b strings.Builder
	for _, r := range clean {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func uniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(in))
	for _, item := range in {
		trim := strings.TrimSpace(item)
		if trim == "" {
			continue
		}
		key := normalizeName(trim)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trim)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func senderDisplay(m chatlog.Message) string {
	if strings.TrimSpace(m.SenderName) != "" {
		return m.SenderName
	}
	if strings.TrimSpace(m.Nickname) != "" {
		return m.Nickname
	}
	if strings.TrimSpace(m.Sender) != "" {
		return m.Sender
	}
	if strings.TrimSpace(m.From) != "" {
		return m.From
	}
	return ""
}

func asciiTokens(s string) []string {
	// Replace non-ASCII letters/digits with spaces
	b := strings.Builder{}
	for _, r := range s {
		if r > 127 {
			b.WriteByte(' ')
			continue
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Fields(b.String())
}

func chineseGrams(s string) []string {
	// Collect contiguous Han sequences, then emit bigrams/trigrams
	grams := []string{}
	seq := []rune{}
	flush := func() {
		if len(seq) >= 2 {
			// bigrams and trigrams
			for n := 2; n <= 3; n++ {
				for i := 0; i+n <= len(seq); i++ {
					grams = append(grams, string(seq[i:i+n]))
				}
			}
		}
		seq = seq[:0]
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			seq = append(seq, r)
		} else {
			flush()
		}
	}
	flush()
	return grams
}

func extractURLs(s string) []string {
	urls := []string{}
	// naive scan for http(s) and split by whitespace
	for _, part := range strings.Fields(s) {
		if strings.HasPrefix(part, "http://") || strings.HasPrefix(part, "https://") {
			// trim trailing punctuation
			part = strings.TrimRight(part, ",.;!?)]}")
			urls = append(urls, part)
		}
	}
	if len(urls) == 0 {
		return urls
	}
	unique := make([]string, 0, len(urls))
	seen := make(map[string]struct{})
	for _, u := range urls {
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		unique = append(unique, u)
	}
	return unique
}

func topK(m map[string]int, k int) []KV {
	arr := make([]KV, 0, len(m))
	for key, c := range m {
		arr = append(arr, KV{Key: key, Count: c})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].Count == arr[j].Count {
			return arr[i].Key < arr[j].Key
		}
		return arr[i].Count > arr[j].Count
	})
	if len(arr) > k {
		arr = arr[:k]
	}
	return arr
}

func topKKeys(m map[string]int, k int) []string {
	arr := make([]KV, 0, len(m))
	for key, c := range m {
		arr = append(arr, KV{Key: key, Count: c})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].Count > arr[j].Count })
	if len(arr) > k {
		arr = arr[:k]
	}
	out := make([]string, 0, len(arr))
	for _, kv := range arr {
		out = append(out, kv.Key)
	}
	return out
}

func runeLen(s string) int { return len([]rune(s)) }

func sprintf(format string, a ...any) string { return strings.TrimSpace(fmtSprintf(format, a...)) }

// inline wrapper to avoid importing fmt at top twice in diffs
func fmtSprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

var (
	stopwordEN = map[string]bool{
		"the": true, "of": true, "and": true, "to": true, "in": true, "is": true, "for": true, "on": true, "with": true, "this": true, "that": true, "are": true, "be": true, "as": true, "by": true, "at": true, "from": true, "or": true, "not": true, "you": true, "your": true,
	}
	stopwordCN = map[string]bool{
		"我们": true, "你们": true, "他们": true, "这个": true, "那个": true, "一个": true, "以及": true, "因为": true, "所以": true, "而且": true, "可以": true, "的话": true, "如果": true, "就是": true, "不是": true, "没有": true, "应该": true, "需要": true, "可能": true, "相关": true, "进行": true, "关于": true, "还有": true, "已经": true,
		"什么": true, "怎么": true, "这种": true, "一些": true, "大家": true, "自己": true, "一下": true, "还是": true, "好的": true,
		"的": true, "了": true, "在": true, "是": true, "和": true, "与": true, "也": true, "都": true, "并": true, "很": true, "更": true, "及": true, "被": true, "就": true, "而": true,
	}
	positiveLexicons = []string{"哈哈", "[微笑]", "👍", "赞", "感谢", "给力", "稳", "太好了", "nice", "great", "perfect", "爽", "牛逼", "加油", "🎉"}
	negativeLexicons = []string{"[捂脸]", "[泪]", "[汗]", "哭", "麻烦", "晕", "糟糕", "不行", "翻车", "崩", "麻了", "难顶", "bug", "问题", "??", "？？", "🙈", "😭", "😓", "😡"}
	positiveEmojiSet = map[string]bool{"微笑": true, "强": true, "赞": true, "OK": true}
	negativeEmojiSet = map[string]bool{"捂脸": true, "汗": true, "泪": true, "抓狂": true, "怒": true}
	nameReplacer     = strings.NewReplacer(
		"\u00a0", "",
		"\u2002", "",
		"\u2003", "",
		"\u2005", "",
		"\u2009", "",
		"\u200a", "",
		"\u200b", "",
		"·", "",
		"•", "",
		"🔆", "",
		"✨", "",
		"🚀", "",
	)
)
