package abstract

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func init() {
	RegisterParser(websearch_setting.ProviderZhipu, restSearchParser{zhipu: true})
	RegisterParser(websearch_setting.ProviderBaiduV2, restSearchParser{})
}

func RESTSearchSupportsBase(channelType int, base string) bool {
	return ChannelWebSearchCapabilities(channelType).SupportsEndpoint(base)
}

func RESTSearchSupportsKey(channelType int, key string) bool {
	return strings.TrimSpace(key) != "" && (channelType != constant.ChannelTypeBaiduV2 || !strings.Contains(key, "|"))
}

type restSearchParser struct{ zhipu bool }

type zhipuSearchHit struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Content string `json:"content"`
	Media   string `json:"media"`
	Icon    string `json:"icon"`
	Date    string `json:"publish_date"`
}

type baiduSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Content string `json:"content"`
	Website string `json:"website"`
	Icon    string `json:"icon"`
	Date    string `json:"date"`
	Type    string `json:"type"`
}

func (parser restSearchParser) ParseSearch(body []byte) ([]Result, error) {
	var results []Result
	if parser.zhipu {
		var wire struct {
			Hits  []zhipuSearchHit `json:"search_result"`
			Error json.RawMessage  `json:"error"`
		}
		if err := common.Unmarshal(body, &wire); err != nil {
			return nil, errors.New("websearch: invalid Zhipu search response")
		}
		if presentJSON(wire.Error) {
			return nil, errors.New("websearch: Zhipu search refused")
		}
		if wire.Hits == nil {
			return nil, errors.New("websearch: missing Zhipu search_result array")
		}
		for _, hit := range wire.Hits {
			results = appendRESTHit(results, Result{Title: hit.Title, URL: hit.Link, Snippet: hit.Content, SiteName: hit.Media, SiteIcon: hit.Icon, Date: hit.Date})
		}
	} else {
		var wire struct {
			Hits []baiduSearchHit `json:"references"`
			Code json.RawMessage  `json:"code"`
		}
		if err := common.Unmarshal(body, &wire); err != nil {
			return nil, errors.New("websearch: invalid Baidu search response")
		}
		if presentJSON(wire.Code) {
			return nil, errors.New("websearch: Baidu search refused")
		}
		if wire.Hits == nil {
			return nil, errors.New("websearch: missing Baidu references array")
		}
		for _, hit := range wire.Hits {
			if hit.Type != "" && hit.Type != "web" {
				continue
			}
			snippet := hit.Snippet
			if strings.TrimSpace(snippet) == "" {
				snippet = hit.Content
			}
			results = appendRESTHit(results, Result{Title: hit.Title, URL: hit.URL, Snippet: snippet, SiteName: hit.Website, SiteIcon: hit.Icon, Date: hit.Date})
		}
	}
	if len(results) == 0 {
		return nil, ErrEmptyResults
	}
	return results, nil
}

func presentJSON(value json.RawMessage) bool {
	return len(value) != 0 && string(value) != "null"
}

func appendRESTHit(results []Result, hit Result) []Result {
	hit.URL = strings.TrimSpace(hit.URL)
	link, err := url.Parse(hit.URL)
	if err != nil || link.Hostname() == "" || (link.Scheme != "https" && link.Scheme != "http") || link.User != nil {
		return results
	}
	date := strings.TrimSpace(hit.Date)
	hit.Date = ""
	if len(date) >= len(time.DateOnly) {
		if parsed, err := time.Parse(time.DateOnly, date[:len(time.DateOnly)]); err == nil {
			hit.Date = parsed.Format(time.DateOnly)
		}
	}
	hit.Authority = -1
	hit.Known = KnownFields{SiteName: hit.SiteName != "", SiteIcon: hit.SiteIcon != "", Date: hit.Date != ""}
	return append(results, hit)
}

func (restSearchParser) ParseFetch([]byte) (Page, error) {
	return Page{}, ErrUnsupportedChannel
}
