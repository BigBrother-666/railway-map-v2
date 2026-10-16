package geo

import "encoding/json"

// SystemGeoStats 是某铁路系统在当前 geojson 中的规模统计。
type SystemGeoStats struct {
	StationCount int     `json:"stationCount"` // 去重后的车站名数量
	TrackKm      float64 `json:"trackKm"`      // 轨道总长（km，双向轨道段只计一次）
}

// SystemGeoStats 按系统统计车站数与轨道长度（每次调用解析一次 geojson，个人中心概览用，调用频率低）。
func (c *Cache) SystemGeoStats() map[string]SystemGeoStats {
	payload, _ := c.Geojson()
	out := map[string]SystemGeoStats{}
	if len(payload) == 0 {
		return out
	}
	var fc struct {
		Features []struct {
			Properties struct {
				Type             string   `json:"type"`
				Name             string   `json:"name"`
				From             string   `json:"from"`
				To               string   `json:"to"`
				Length           float64  `json:"length"`
				RailwaySystemID  string   `json:"railwaySystemId"`
				RailwaySystemIDs []string `json:"railwaySystemIds"`
			} `json:"properties"`
			Geometry struct {
				Type string `json:"type"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if json.Unmarshal(payload, &fc) != nil {
		return out
	}
	stations := map[string]map[string]struct{}{}
	meters := map[string]float64{}
	seenEdge := map[string]struct{}{}
	for _, f := range fc.Features {
		p := f.Properties
		switch f.Geometry.Type {
		case "Point":
			if p.Type != "station" || p.Name == "" {
				continue
			}
			for _, sys := range p.RailwaySystemIDs {
				if stations[sys] == nil {
					stations[sys] = map[string]struct{}{}
				}
				stations[sys][p.Name] = struct{}{}
			}
		case "LineString":
			if p.RailwaySystemID == "" {
				continue
			}
			a, b := p.From, p.To
			if a > b {
				a, b = b, a
			}
			key := p.RailwaySystemID + "|" + a + "|" + b
			if _, dup := seenEdge[key]; dup {
				continue
			}
			seenEdge[key] = struct{}{}
			meters[p.RailwaySystemID] += p.Length
		}
	}
	for sys, names := range stations {
		st := out[sys]
		st.StationCount = len(names)
		out[sys] = st
	}
	for sys, m := range meters {
		st := out[sys]
		st.TrackKm = round2(m / 1000)
		out[sys] = st
	}
	return out
}

// StationNames 返回当前 geojson 中全部车站名（去重，供车站别名 / 车站组编辑时补全与校验）。
func (c *Cache) StationNames() []string {
	payload, _ := c.Geojson()
	if len(payload) == 0 {
		return []string{}
	}
	var fc struct {
		Features []struct {
			Properties struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"properties"`
			Geometry struct {
				Type string `json:"type"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if json.Unmarshal(payload, &fc) != nil {
		return []string{}
	}
	seen := map[string]struct{}{}
	names := []string{}
	for _, f := range fc.Features {
		if f.Geometry.Type != "Point" || f.Properties.Type != "station" || f.Properties.Name == "" {
			continue
		}
		if _, ok := seen[f.Properties.Name]; ok {
			continue
		}
		seen[f.Properties.Name] = struct{}{}
		names = append(names, f.Properties.Name)
	}
	return names
}
