package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
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

// SocksProxy handles the connection
type SocksProxy struct {
	version     uint8
	conn        net.Conn
	command     socksCommand
	addressType socksAddressType
	reply       socksReply
	IP          net.IP
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
	s.IP = nil
	return &s
}

func (s *SocksProxy) closeConnection() {
	log.Printf("Closing connection from %s", s.conn.RemoteAddr())
	s.conn.Close()
}

func (s *SocksProxy) closeConnectionWithError(err socksReply) {
	log.Printf("Closing connection with error %v", err)
	s.reply = err
	payload := s.generateFailedReply([]byte{0}, 0)
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
		s.IP = net.IP(buf)
	} else if s.addressType == domainName {
		domainLength := []byte{0}
		s.conn.Read(domainLength)
		fqdn := make([]byte, int(domainLength[0]))
		s.conn.Read(fqdn)
		s.FQDN = string(fqdn)
	} else if s.addressType == ipv6 {
		buf := make([]byte, 16)
		s.conn.Read(buf)
		s.IP = net.IP(buf)
	} else {
		log.Printf("Unknown address type")
		s.closeConnectionWithError(addressTypeNotSupported)
	}
}

func (s *SocksProxy) parsePort() {
	buf := []byte{0, 0}
	s.conn.Read(buf)
	s.port = (uint16(buf[0]) << 8) | uint16(buf[1])
}

func (s *SocksProxy) handleGreetings() {
	version := []byte{0}
	nmethod := []byte{0}
	s.conn.Read(version)
	s.conn.Read(nmethod)

	s.ensureVersion(version[0])
	s.ensureNMethod(nmethod[0])

	s.methods = s.getAvailableMethods(nmethod[0])

	s.conn.Write([]byte{socksVersion, uint8(noAuth)})
}

func (s *SocksProxy) handleRequestHeader() {
	// version, command, RESERVED, address_type
	header := []byte{0, 0, 0, 0}
	s.conn.Read(header)

	s.ensureVersion(header[0])
	s.command = socksCommand(header[1])
	s.addressType = socksAddressType(header[3])

	// may set s.IP or s.FQDN
	s.parseAddress()
	s.parsePort()
}

// remote address to be dialed
func (s *SocksProxy) constructRemoteAddress() string {
	remoteAddress := ""
	if s.addressType == ipv4 || s.addressType == ipv6 {
		remoteAddress = fmt.Sprintf("%v:%d", s.IP, s.port)
	} else if s.addressType == domainName {
		// resolve domain name to ipv4
		ips, err := net.LookupIP(s.FQDN)
		if err != nil {
			log.Printf("Closing ... could not resolve FQDN %s", s.FQDN)
			s.closeConnectionWithError(generalFailure)
		}

		remoteAddress = fmt.Sprintf("%s:%d", ips[0], s.port)

		// we are now IPv4
		s.addressType = ipv4
	} else {
		log.Printf("Closing ... address type not supported")
		s.closeConnectionWithError(addressTypeNotSupported)
	}

	return remoteAddress
}

func (s *SocksProxy) handleCommandConnect() []byte {
	remoteAddress := s.constructRemoteAddress()
	remote, err := net.Dial("tcp", remoteAddress)
	s.remote = remote

	if err != nil {
		log.Printf("Failed to dial remote: %s", remoteAddress)

		msg := err.Error()
		if strings.Contains(msg, "network is unreachable") {
			s.closeConnectionWithError(networkUnreachable)
		} else if strings.Contains(msg, "refused") {
			s.closeConnectionWithError(connectionRefused)
		} else {
			s.closeConnectionWithError(hostUnreachable)
		}
	}

	bindAddress := remote.LocalAddr().(*net.TCPAddr)
	log.Printf("Connecting to: %s, binding to: %v", remoteAddress, bindAddress)

	s.reply = succeeded
	return s.generateSucceededReply(bindAddress.IP, bindAddress.Port)
}

func (s *SocksProxy) generateReply(ip net.IP, port int) []byte {
	payload := []byte{socksVersion, uint8(s.reply), 0, uint8(s.addressType)}
	payload = append(payload, ip...)
	payload = append(payload, uint8(port>>8))
	payload = append(payload, uint8(port&0xff))
	return payload
}

func (s *SocksProxy) generateSucceededReply(ip net.IP, port int) []byte {
	return s.generateReply(ip, port)
}
func (s *SocksProxy) generateFailedReply(ip net.IP, port int) []byte {
	return s.generateReply(ip, port)
}

func (s *SocksProxy) handleRequestCommand() ([]byte, error) {
	if s.command == connect {
		return s.handleCommandConnect(), nil
	} else if s.command == bind {
		log.Printf("Bind command is not supproted")
		s.closeConnectionWithError(commandNotSupported)
	} else if s.command == udpAssociate {
		log.Printf("UDP associate command is not supproted")
		s.closeConnectionWithError(commandNotSupported)
	} else {
		log.Printf("Unknown command")
		s.closeConnectionWithError(commandNotSupported)
	}
	return nil, errors.New("Unsupported command")
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
	proxy := func(dst net.Conn, src net.Conn, errCh chan error) {
		_, err := io.Copy(dst, src)
		if err != nil {
			log.Printf("Error on proxy: %v", err)
		}
		errCh <- err
	}

	errCh := make(chan error, 2)
	go proxy(remote, client, errCh)
	go proxy(client, remote, errCh)

	// wait until proxy complete
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			return err
		}
	}

	return nil
}

func handle(conn net.Conn) {
	server := NewProxy(conn)
	defer server.closeConnection()

	log.Printf("Accepting connection from: %s", conn.RemoteAddr())

	server.handleGreetings()
	server.handleRequestHeader()
	payload, err := server.handleRequestCommand()
	if err != nil {
		log.Printf("Error on handleRequestCommand: %v", err)
	}
	if len(payload) > 0 {
		if _, err := server.conn.Write(payload); err != nil {
			log.Printf("Error on Sending back request: %v", err)
		}
	}

	if err := server.doReplyAction(); err != nil {
		log.Printf("Error on doReplyAction: %v", err)
	}

	log.Printf("Closing remote: %s", server.remote.RemoteAddr())
	if server.remote != nil {
		server.remote.Close()
	}
}

func serve() {
	service := fmt.Sprintf("localhost:%d", port)
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
