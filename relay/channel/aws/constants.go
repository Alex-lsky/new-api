package aws

var awsModelIDMap = map[string]string{
	"claude-instant-1.2":         "anthropic.claude-instant-v1",
	"claude-2.0":                 "anthropic.claude-v2",
	"claude-2.1":                 "anthropic.claude-v2:1",
	"claude-3-sonnet-20240229":   "anthropic.claude-3-sonnet-20240229-v1:0",
	"claude-3-opus-20240229":     "anthropic.claude-3-opus-20240229-v1:0",
	"claude-3-haiku-20240307":    "anthropic.claude-3-haiku-20240307-v1:0",
	"claude-3-5-sonnet-20240620": "anthropic.claude-3-5-sonnet-20240620-v1:0",
	"claude-3-5-sonnet-20241022": "anthropic.claude-3-5-sonnet-20241022-v2:0",
	"claude-3-5-haiku-20241022":  "anthropic.claude-3-5-haiku-20241022-v1:0",
	"command-r":                  "cohere.command-r-v1:0",
	"command-r-plus":             "cohere.command-r-plus-v1:0",
	"mistral-7b":                 "mistral.mistral-7b-instruct-v0:2",
	"mixtral-8x7b":               "mistral.mixtral-8x7b-instruct-v0:1",
	"llama2-13b":                 "meta.llama2-13b-chat-v1",
	"llama2-70b":                 "meta.llama2-70b-chat-v1",
	"llama3-8b":                  "meta.llama3-8b-instruct-v1:0",
	"llama3-70b":                 "meta.llama3-70b-instruct-v1:0",
	"titan-text":                 "amazon.titan-text-express-v1",
	"titan-text-lite":            "amazon.titan-text-lite-v1",
	"nova-micro":                 "amazon.nova-micro-v1",
	"nova-lite":                  "amazon.nova-lite-v1",
	"nova-pro":                   "amazon.nova-pro-v1",
	"jamba-instruct":             "ai21.jamba-instruct-v1",
	"jamba-1.5-large":            "ai21.jamba-1.5-large-v1",
	"jamba-1.5-mini":             "ai21.jamba-1.5-mini-v1",
	"mistral-large":              "mistral.mistral-large-v1",
	"mistral-large-2":            "mistral.mistral-large-2-v1",
	"mistral-small":              "mistral.mistral-small-v1",
	"llama3-1":                   "meta.llama3-1-v1",
	"llama3-2-1b":                "meta.llama3-2-1b-v1",
	"llama3-2-3b":                "meta.llama3-2-3b-v1",
	"llama3-2-11b":               "meta.llama3-2-11b-v1",
	"llama3-2-90b":               "meta.llama3-2-90b-v1",
}

var ChannelName = "aws"
