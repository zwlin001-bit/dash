package aliyun

import "dash/internal/provider"

// DefaultBuiltinRegions provides a fallback list of common Aliyun regions with Chinese local names.
func DefaultBuiltinRegions() []provider.Region {
	return []provider.Region{
		{RegionID: "cn-hangzhou", LocalName: "华东1（杭州）"},
		{RegionID: "cn-shanghai", LocalName: "华东2（上海）"},
		{RegionID: "cn-beijing", LocalName: "华北2（北京）"},
		{RegionID: "cn-shenzhen", LocalName: "华南1（深圳）"},
		{RegionID: "cn-hongkong", LocalName: "中国香港"},
		{RegionID: "ap-southeast-1", LocalName: "新加坡"},
		{RegionID: "ap-northeast-1", LocalName: "日本（东京）"},
		{RegionID: "us-west-1", LocalName: "美国（硅谷）"},
		{RegionID: "us-east-1", LocalName: "美国（弗吉尼亚）"},
		{RegionID: "eu-central-1", LocalName: "德国（法兰克福）"},
		{RegionID: "eu-west-1", LocalName: "英国（伦敦）"},
		{RegionID: "ap-south-1", LocalName: "印度（孟买）"},
		{RegionID: "ap-southeast-3", LocalName: "马来西亚（吉隆坡）"},
	}
}
