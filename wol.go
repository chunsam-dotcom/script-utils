package main

import (
	"flag"
	"fmt"
	"net"
	"os"
)

func sendWOL(macAddr string) error {
	// 1. MAC 주소 유효성 검사 및 파싱
	hwAddr, err := net.ParseMAC(macAddr)
	if err != nil {
		return fmt.Errorf("유효하지 않은 MAC 주소입니다: %v", err)
	}

	// 2. 매직 패킷 구성 (0xFF*6 + MAC*16)
	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:(i+1)*6], hwAddr)
	}

	// 3. 브로드캐스트 주소 설정 (UDP 9번 포트)
	addr, err := net.ResolveUDPAddr("udp", "255.255.255.255:9")
	if err != nil {
		return err
	}

	// 4. 패킷 전송
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	return err
}

func main() {
	// flag 패키지를 사용해 깔끔하게 인자 처리
	macPtr := flag.String("mac", "", "대상 기기의 MAC 주소 (예: AA:BB:CC:DD:EE:FF)")
	flag.Parse()

	if *macPtr == "" {
		fmt.Println("사용법: wol -mac [MAC_ADDRESS]")
		os.Exit(1)
	}

	err := sendWOL(*macPtr)
	if err != nil {
		fmt.Printf("오류 발생: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("성공: %s 주소로 매직 패킷을 전송했습니다.\n", *macPtr)
}
