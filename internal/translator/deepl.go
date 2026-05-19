package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	APIKey     string
	APIURL     string
	SourceLang string
	TargetLang string
	HTTP       *http.Client
}

func NewClient(apiKey, apiURL, sourceLang, targetLang string) *Client {
	return &Client{
		APIKey:     apiKey,
		APIURL:     apiURL,
		SourceLang: sourceLang,
		TargetLang: targetLang,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}
}

type deeplResp struct {
	Translations []struct {
		Text string `json:"text"`
	} `json:"translations"`
}

// Translate translates each input string and returns translations in the same order.
// Pass at most 50 texts per call (DeepL limit).
func (c *Client) Translate(texts []string) ([]string, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("deepl api key not configured")
	}
	form := url.Values{}
	for _, t := range texts {
		form.Add("text", t)
	}
	form.Set("source_lang", c.SourceLang)
	form.Set("target_lang", c.TargetLang)

	req, err := http.NewRequest("POST", c.APIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "DeepL-Auth-Key "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("deepl %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var dr deeplResp
	if err := json.Unmarshal(body, &dr); err != nil {
		return nil, fmt.Errorf("decode deepl response: %w", err)
	}
	if len(dr.Translations) != len(texts) {
		return nil, fmt.Errorf("deepl returned %d translations for %d inputs", len(dr.Translations), len(texts))
	}
	out := make([]string, len(texts))
	for i, t := range dr.Translations {
		out[i] = t.Text
	}
	return out, nil
}
