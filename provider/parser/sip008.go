package parser

import (
	"context"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
)

type ShadowsocksDocument struct {
	Version int                         `json:"version"`
	Servers []ShadowsocksServerDocument `json:"servers"`
}

type ShadowsocksServerDocument struct {
	ID         string `json:"id"`
	Remarks    string `json:"remarks"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
	Plugin     string `json:"plugin"`
	PluginOpts string `json:"plugin_opts"`
}

func ParseSIP008Subscription(_ context.Context, content string) ([]option.Outbound, []option.Endpoint, error) {
	var document ShadowsocksDocument
	err := json.Unmarshal([]byte(content), &document)
	if err != nil {
		return nil, nil, E.Cause(err, "parse SIP008 document")
	}

	var servers []option.Outbound
	for i, server := range document.Servers {
		// A null entry decodes to the zero value. Publishing it would create an outbound with no
		// server, no port and no method - a node that can only fail later, with an error that no
		// longer names the document that produced it. Skip it instead; when every entry is invalid
		// the reader returns no nodes and the format-detection chain reports the document as
		// unrecognised, which keeps the provider's last known good set.
		if server.Server == "" || server.ServerPort <= 0 || server.Method == "" {
			continue
		}
		if server.ServerPort > 65535 {
			return nil, nil, E.New("SIP008 server[", i, "] has an out-of-range port: ", server.ServerPort)
		}
		servers = append(servers, option.Outbound{
			Type: C.TypeShadowsocks,
			Tag:  server.Remarks,
			Options: &option.ShadowsocksOutboundOptions{
				ServerOptions: option.ServerOptions{
					Server:     server.Server,
					ServerPort: uint16(server.ServerPort),
				},
				Password:      server.Password,
				Method:        server.Method,
				Plugin:        server.Plugin,
				PluginOptions: server.PluginOpts,
			},
		})
	}
	return servers, nil, nil
}
