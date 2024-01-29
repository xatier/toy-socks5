package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
)

// constants
const (
	socksVersion = uint8(5)
	port         = 1081
)

// SocksMethod is the authentication method to be used.
// noAuth means no authentication at all
type socksMethod uint8

const (
	noAuth socksMethod = socksMethod(0x00)
)

type socksCommand uint8

const (
	connect      = socksCommand(0x01)
	bind         = socksCommand(0x02)
	udpAssociate = socksCommand(0x03)
)

type socksAddressType uint8

const (
	ipv4       = socksAddressType(0x01)
	domainName = socksAddressType(0x03)
	ipv6       = socksAddressType(0x04)
)

type socksReply uint8

const (
	succeeded               = socksReply(0x00)
	generalFailure          = socksReply(0x01)
	connectionNotAllowed    = socksReply(0x02)
	networkUnreachable      = socksReply(0x03)
	hostUnreachable         = socksReply(0x04)
	connectionRefused       = socksReply(0x05)
	ttlExpired              = socksReply(0x06)
	commandNotSupported     = socksReply(0x07)
	addressTypeNotSupported = socksReply(0x08)
)

func (r socksReply) String() string {
	switch r {
	case succeeded:
		return "succeeded"
	case generalFailure:
		return "general failure"
	case connectionNotAllowed:
		return "connection not allowed"
	case networkUnreachable:
		return "network unreachable"
	case hostUnreachable:
		return "host unreachable"
	case connectionRefused:
		return "connection refused"
	case ttlExpired:
		return "ttl expired"
	case commandNotSupported:
		return "command not supported"
	case addressTypeNotSupported:
		return "address type not supported"
	default:
		return fmt.Sprintf("unknown(%d)", r)
	}
}

// SocksProxy handles the connection
type SocksProxy struct {
	version     uint8
	conn        net.Conn
	command     socksCommand
	addressType socksAddressType
	reply       socksReply
	IP          netip.Addr
	FQDN        string
	port        uint16
	remote      net.Conn
	methods     []uint8
}

// NewProxy creates a SocksProxy
func NewProxy(c net.Conn) *SocksProxy {
	s := SocksProxy{}
	s.version = uint8(5)
	s.conn = c
	s.IP = netip.Addr{}
	return &s
}

func (s *SocksProxy) closeConnection() {
	if s.conn == nil {
		return
	}
	log.Printf("Closing connection from %s", s.conn.RemoteAddr())
	s.conn.Close()
	s.conn = nil
}

func (s *SocksProxy) closeConnectionWithError(reply socksReply) {
	log.Printf("Closing connection with error %v", reply)
	// avoid write if connection is already closed
	if s.conn == nil {
		return
	}
	s.reply = reply
	payload := s.generateFailedReply(netip.MustParseAddrPort("0.0.0.0:0"))

	s.conn.Write(payload)
	s.closeConnection()
}

func (s *SocksProxy) ensureVersion(version uint8) {
	if version != socksVersion {
		s.closeConnectionWithError(generalFailure)
	}
}

func (s *SocksProxy) ensureNMethod(nmethod uint8) {
	if !(1 <= nmethod && nmethod <= 255) {
		s.closeConnectionWithError(generalFailure)
	}
}

func (s *SocksProxy) getAvailableMethods(nmethod uint8) []uint8 {
	methods := make([]uint8, nmethod)
	m := []byte{0}
	for i := uint8(0); i < nmethod; i++ {
		s.conn.Read(m)
		methods[i] = m[0]
	}
	return methods
}

func (s *SocksProxy) parseAddress() {
	if s.addressType == ipv4 {
		buf := make([]byte, 4)
		s.conn.Read(buf)
		s.IP = netip.AddrFrom4(*(*[4]byte)(buf))
	} else if s.addressType == domainName {
		domainLength := []byte{0}
		s.conn.Read(domainLength)
		fqdn := make([]byte, int(domainLength[0]))
		s.conn.Read(fqdn)
		s.FQDN = string(fqdn)
	} else if s.addressType == ipv6 {
		buf := make([]byte, 16)
		s.conn.Read(buf)
		s.IP = netip.AddrFrom16(*(*[16]byte)(buf))
	} else {
		log.Printf("Unknown address type (%d)", s.addressType)
		s.closeConnectionWithError(addressTypeNotSupported)
	}
}

func (s *SocksProxy) parsePort() {
	buf := []byte{0, 0}
	s.conn.Read(buf)
	s.port = (uint16(buf[0]) << 8) | uint16(buf[1])
}

func (s *SocksProxy) handleGreetings() error {
	version := []byte{0}
	nmethod := []byte{0}
	s.conn.Read(version)
	s.conn.Read(nmethod)

	log.Printf("handleGreetings: version=%d", version[0])
	//s.ensureVersion(version[0])
	if s.conn == nil {
		return fmt.Errorf("connection closed due to invalid version(%d)", version[0])
	}
	s.ensureNMethod(nmethod[0])
	if s.conn == nil {
		return fmt.Errorf("connection closed due to invalid nmethod(%d)", nmethod[0])
	}

	s.methods = s.getAvailableMethods(nmethod[0])

	s.conn.Write([]byte{socksVersion, uint8(noAuth)})
	return nil
}

func (s *SocksProxy) handleRequestHeader() error {
	// version, command, RESERVED, address_type
	header := []byte{0, 0, 0, 0}
	s.conn.Read(header)

	s.ensureVersion(header[0])
	if s.conn == nil {
		return fmt.Errorf("connection closed due to invalid version(%d)", header[0])
	}
	s.command = socksCommand(header[1])
	s.addressType = socksAddressType(header[3])

	// may set s.IP or s.FQDN
	s.parseAddress()
	if s.conn == nil {
		return fmt.Errorf("connection closed due to invalid address type(%d)", header[3])
	}
	s.parsePort()
	return nil
}

// remote address to be dialed
func (s *SocksProxy) constructRemoteAddress() string {
	remoteAddress := ""
	if s.addressType == ipv4 || s.addressType == ipv6 {
		remoteAddress = fmt.Sprintf("%v:%d", s.IP, s.port)
	} else if s.addressType == domainName {
		// resolve domain name to ipv4
		ips, err := net.LookupIP(s.FQDN)
		if err != nil || len(ips) == 0 {
			log.Printf("Closing ... could not resolve FQDN %s", s.FQDN)
			s.closeConnectionWithError(generalFailure)
		}
		if len(ips) > 0 {
			log.Printf("Resolving %s:%d -> %s:%d %v", s.FQDN, s.port, ips[0], s.port, ips)

			remoteAddress = fmt.Sprintf("%s:%d", ips[0], s.port)

			// we are now IPv4
			s.addressType = ipv4
		}
	} else {
		log.Printf("Closing ... address type (%d) not supported", s.addressType)
		s.closeConnectionWithError(addressTypeNotSupported)
	}

	return remoteAddress
}

func (s *SocksProxy) handleCommandConnect() ([]byte, error) {
	remoteAddress := s.constructRemoteAddress()
	// server may be closed early due to error
	if s.conn == nil {
		return nil, fmt.Errorf("Connection closed early")
	}
	remote, err := net.Dial("tcp", remoteAddress)
	s.remote = remote

	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "network is unreachable") {
			s.closeConnectionWithError(networkUnreachable)
		} else if strings.Contains(msg, "refused") {
			s.closeConnectionWithError(connectionRefused)
		} else {
			s.closeConnectionWithError(hostUnreachable)
		}
		return nil, fmt.Errorf("Failed to dial remote: %s: %v", remoteAddress, err)
	}

	bindAddress := remote.LocalAddr().(*net.TCPAddr)
	log.Printf("Connecting to: %s, binding to: %v", remoteAddress, bindAddress)

	s.reply = succeeded
	return s.generateSucceededReply(bindAddress.AddrPort()), nil
}

func (s *SocksProxy) generateReply(addrPort netip.AddrPort) []byte {
	if !addrPort.IsValid() {
		log.Printf("Invalid address: %v", addrPort)
		return []byte{}
	}

	ip := addrPort.Addr().AsSlice()
	port := addrPort.Port()
	payload := []byte{socksVersion, uint8(s.reply), 0, uint8(s.addressType)}
	payload = append(payload, ip...)
	payload = append(payload, uint8(port>>8))
	payload = append(payload, uint8(port&0xff))
	return payload
}

func (s *SocksProxy) generateSucceededReply(addrPort netip.AddrPort) []byte {
	return s.generateReply(addrPort)
}

func (s *SocksProxy) generateFailedReply(addrPort netip.AddrPort) []byte {
	return s.generateReply(addrPort)
}

func (s *SocksProxy) handleRequestCommand() ([]byte, error) {
	if s.command == connect {
		reply, err := s.handleCommandConnect()
		if err != nil {
			return nil, err
		}
		return reply, nil
	} else if s.command == bind {
		log.Printf("Bind command is not supported")
		s.closeConnectionWithError(commandNotSupported)
	} else if s.command == udpAssociate {
		log.Printf("UDP associate command is not supported")
		s.closeConnectionWithError(commandNotSupported)
	} else {
		log.Printf("Unknown command")
		s.closeConnectionWithError(commandNotSupported)
	}
	return nil, fmt.Errorf("Unsupported command (%d)", s.command)
}

func (s *SocksProxy) doReplyAction() error {
	if s.command == connect {
		if s.reply == succeeded {
			if err := exchange(s.conn, s.remote); err != nil {
				log.Printf("Error on exchange: %v", err)
				return err
			}
		}
	}
	return nil
}

// exchange data between two net.Conn
func exchange(client net.Conn, remote net.Conn) error {
	var wg sync.WaitGroup
	wg.Add(2)

	proxy := func(dst net.Conn, src net.Conn) {
		_, err := io.Copy(dst, src)
		if err != nil {
			log.Printf("Error on proxy: %v", err)
		}
		dst.(*net.TCPConn).CloseWrite()
		wg.Done()
	}

	go proxy(remote, client)
	go proxy(client, remote)

	wg.Wait()

	return nil
}

func handle(conn net.Conn) {
	server := NewProxy(conn)
	defer server.closeConnection()

	log.Printf("Accepting connection from: %s", conn.RemoteAddr())

	err := server.handleGreetings()
	if err != nil {
		log.Printf("Connection closed early on handleGreetings, %v", err)
		return
	}
	err = server.handleRequestHeader()
	if err != nil {
		log.Printf("Connection closed early on handleRequestHeader, %v", err)
		return
	}

	payload, err := server.handleRequestCommand()
	if err != nil {
		log.Printf("Error on handleRequestCommand: %v", err)
		return
	}
	if len(payload) > 0 {
		if _, err := server.conn.Write(payload); err != nil {
			log.Printf("Error on Sending back request: %v", err)
			return
		}
	}

	if err := server.doReplyAction(); err != nil {
		log.Printf("Error on doReplyAction: %v", err)
		return
	}

	if server.remote != nil {
		log.Printf("Closing remote: %s", server.remote.RemoteAddr())
		server.remote.Close()
	}
}

func serve() {
	isGlobal := flag.Bool("global", false, "Use -global to listen on 0.0.0.0")
	flag.Parse()

	// use -global flag to listen on 0.0.0.0
	bindAddress := "localhost"
	if *isGlobal {
		bindAddress = "0.0.0.0"
	}

	service := fmt.Sprintf("%s:%d", bindAddress, port)
	log.Printf("Running on: %s", service)

	listener, err := net.Listen("tcp", service)
	if err != nil {
		log.Fatalf("Can't listen to %s: %v", service, err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalf("Can't accept to %s: %v", service, err)
			continue
		}
		go handle(conn)
	}
}

func main() {
	serve()
}
