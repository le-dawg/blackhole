import re

with open("src/main.go", "r") as f:
    content = f.read()

# Update forwardQuery calls
content = content.replace("forwardQuery(buf[:n], cliAddr, conn)", "forwardQuery(buf[:n], cliAddr, conn, msg, domain)")

# Replace forwardQuery definition
new_forwardQuery = """func forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn, msg dnsmessage.Message, domain string) {
    if len(msg.Questions) > 0 {
        q := msg.Questions[0]
        if cachedMsg, ok := dnsCache.Get(domain, uint16(q.Type)); ok {
            cachedMsg.Header.ID = msg.Header.ID
            resp, err := cachedMsg.Pack()
            if err == nil {
                _, _ = conn.WriteToUDP(resp, cliAddr)
                return
            }
        }
    }

    upstreamsMu.RLock()
    currentUpstreams := upstreams
    upstreamsMu.RUnlock()

    respRaw, err := dnsd.RaceForward(raw, currentUpstreams, 500*time.Millisecond)
    if err == nil {
        var respMsg dnsmessage.Message
        if unpackErr := respMsg.Unpack(respRaw); unpackErr == nil && len(respMsg.Questions) > 0 {
            dnsCache.Set(domain, uint16(respMsg.Questions[0].Type), &respMsg)
        }
        _, _ = conn.WriteToUDP(respRaw, cliAddr)
    }
}"""

# Use regex to replace the old forwardQuery definition
# The old one is: func forwardQuery(raw []byte, cliAddr *net.UDPAddr, conn *net.UDPConn) { ... }
# Find from func forwardQuery to the next func sendBlockedResponse

pattern = re.compile(r"func forwardQuery\(raw \[\]byte, cliAddr \*net\.UDPAddr, conn \*net\.UDPConn\).*?(?=func sendBlockedResponse)", re.DOTALL)
content = pattern.sub(new_forwardQuery + "\n\n", content)

with open("src/main.go", "w") as f:
    f.write(content)
