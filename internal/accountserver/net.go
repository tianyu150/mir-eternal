package accountserver

import "net"

func splitHostPort(value string) (string, string, error) { return net.SplitHostPort(value) }
