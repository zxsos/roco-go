// Package socks5 提供极简的 RFC 1928 SOCKS5 代理服务器(仅 TCP CONNECT,无认证)。
// 面向"云服务器当网关抓自己进程出站流量"的部署场景:手机把游戏流量代理到本机,
// rocom-capture 整网卡抓包即可看到代理进程以本机 IP 出站的连接(须配合 -skip-self-ip=false)。
package socks5

import (
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

const (
	verSocks5      = 0x05
	methodNone     = 0x00 // 无认证(裸代理,部署方自行决定安全边界)
	cmdConnect     = 0x01 // 仅支持 CONNECT,TCP 游戏流量用不到 BIND/UDP
	atypIPv4       = 0x01
	atypDomain     = 0x03
	atypIPv6       = 0x04
	repSuccess     = 0x00
	repConnRefused = 0x05
)

// ListenAndServe 在 addr 上监听并处理 SOCKS5 连接,阻塞直至监听器关闭。
func ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("socks5 代理已启动: %s (无认证)", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(conn)
	}
}

// handle 处理单个客户端连接:握手 → 拨号上游 → 双向转发。
func handle(c net.Conn) {
	defer c.Close()
	target, err := handshake(c)
	if err != nil {
		log.Printf("socks5: %s 握手失败: %v", c.RemoteAddr(), err)
		return
	}
	up, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		writeReply(c, repConnRefused) // 回复失败,客户端据此断开
		log.Printf("socks5: 连接 %s 失败: %v", target, err)
		return
	}
	defer up.Close()
	if err := writeReply(c, repSuccess); err != nil {
		return
	}
	log.Printf("socks5: %s → %s", c.RemoteAddr(), target)
	relay(c, up)
	log.Printf("socks5: 断开 %s → %s", c.RemoteAddr(), target)
}

// handshake 完成版本协商与 CONNECT 请求解析,返回目标 "host:port"。
func handshake(c net.Conn) (string, error) {
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return "", err
	}
	if hdr[0] != verSocks5 {
		return "", errors.New("非 socks5 版本")
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", err
	}
	if _, err := c.Write([]byte{verSocks5, methodNone}); err != nil {
		return "", err
	}
	var req [4]byte // VER CMD RSV ATYP
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return "", err
	}
	if req[0] != verSocks5 || req[2] != 0 {
		return "", errors.New("非法请求头")
	}
	if req[1] != cmdConnect {
		return "", errors.New("仅支持 CONNECT")
	}
	var host string
	switch req[3] {
	case atypIPv4:
		var ip [4]byte
		if _, err := io.ReadFull(c, ip[:]); err != nil {
			return "", err
		}
		host = net.IP(ip[:]).String()
	case atypDomain:
		var ln [1]byte
		if _, err := io.ReadFull(c, ln[:]); err != nil {
			return "", err
		}
		dm := make([]byte, int(ln[0]))
		if _, err := io.ReadFull(c, dm); err != nil {
			return "", err
		}
		host = string(dm)
	case atypIPv6:
		var ip [16]byte
		if _, err := io.ReadFull(c, ip[:]); err != nil {
			return "", err
		}
		host = net.IP(ip[:]).String()
	default:
		return "", errors.New("非法地址类型")
	}
	var port [2]byte
	if _, err := io.ReadFull(c, port[:]); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port[0])<<8|int(port[1]))), nil
}

// writeReply 回 CONNECT 应答(BND.ADDR 固定 0.0.0.0:0,客户端不关心)。
func writeReply(c net.Conn, rep byte) error {
	_, err := c.Write([]byte{verSocks5, rep, 0, atypIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

// relay 双向转发,任一端结束即关闭双方连接。
func relay(a, b net.Conn) {
	done := make(chan struct{}, 1)
	go func() { io.Copy(b, a); done <- struct{}{} }()
	go func() { io.Copy(a, b); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
}
