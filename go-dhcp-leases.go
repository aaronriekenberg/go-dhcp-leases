package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

var logger = log.New(os.Stdout, "", 0)

const (
	leasesFile            = "dhcpd.leases"
	leaseTimeFormatString = "2006/01/02 15:04:05;"
	ouputTimeFormatString = "2006/01/02 15:04:05 -0700"
)

type leaseInfo struct {
	ipAddress  net.IP
	count      int
	startTime  time.Time
	endTime    time.Time
	clttTime   time.Time
	macAddress net.HardwareAddr
	hostname   string
}

func (li *leaseInfo) String() string {
	return fmt.Sprintf("ipAddress=%v startTime=%v endTime=%v clttTime=%v macAddress=%v hostname=%v", li.ipAddress.String(), li.startTime, li.endTime, li.clttTime, li.macAddress.String(), li.hostname)
}

type leaseMap map[string]*leaseInfo

func readLeasesFile() leaseMap {
	leaseMap := make(leaseMap)

	logger.Printf("reading %v", leasesFile)
	file, err := os.OpenFile(leasesFile, os.O_RDONLY, os.ModePerm)
	if err != nil {
		logger.Fatalf("Failed to open file %v %s\n", leasesFile, err.Error())
	}
	defer file.Close()

	lineNumber := 0
	var currentIP net.IP
	var currentLeaseInfo *leaseInfo
	withinLease := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNumber++

		line := strings.TrimSpace(scanner.Text())

		if !withinLease {
			if strings.HasPrefix(line, "lease ") && strings.HasSuffix(line, " {") {
				currentIP = net.ParseIP(strings.Split(line, " ")[1])
				currentLeaseInfo = &leaseInfo{
					ipAddress: currentIP,
					count:     1,
				}
				withinLease = true
			}
		} else {
			if strings.HasPrefix(line, "starts") {
				split := strings.Split(line, " ")
				timeString := split[2] + " " + split[3]
				startTime, err := time.ParseInLocation(leaseTimeFormatString, timeString, time.Local)
				if err != nil {
					logger.Fatalf("error parsing start timeString '%v' %v", timeString, err.Error())
				}
				currentLeaseInfo.startTime = startTime
			} else if strings.HasPrefix(line, "ends") {
				split := strings.Split(line, " ")
				timeString := split[2] + " " + split[3]
				endTime, err := time.ParseInLocation(leaseTimeFormatString, timeString, time.Local)
				if err != nil {
					logger.Fatalf("error parsing end timeString '%v' %v", timeString, err.Error())
				}
				currentLeaseInfo.endTime = endTime
			} else if strings.HasPrefix(line, "cltt") {
				split := strings.Split(line, " ")
				timeString := split[2] + " " + split[3]
				clttTime, err := time.ParseInLocation(leaseTimeFormatString, timeString, time.Local)
				if err != nil {
					logger.Fatalf("error parsing cltt timeString '%v' %v", timeString, err.Error())
				}
				currentLeaseInfo.clttTime = clttTime
			} else if strings.HasPrefix(line, "hardware ethernet ") {
				macString := strings.Split(strings.Split(line, " ")[2], ";")[0]
				macAddress, err := net.ParseMAC(macString)
				if err != nil {
					logger.Fatalf("error parsing macString '%v' %v", macString, err.Error())
				}
				currentLeaseInfo.macAddress = macAddress
			} else if strings.HasPrefix(line, "client-hostname ") {
				hostname := strings.Split(line, "\"")[1]
				currentLeaseInfo.hostname = hostname
			} else if strings.HasPrefix(line, "}") {
				withinLease = false
				if currentLeaseInfo != nil {
					ipString := currentIP.String()
					existingLeaseInfo, ok := leaseMap[ipString]
					if ok {
						totalCount := currentLeaseInfo.count + existingLeaseInfo.count
						if currentLeaseInfo.endTime.After(existingLeaseInfo.endTime) {
							currentLeaseInfo.count = totalCount
							leaseMap[ipString] = currentLeaseInfo
						} else {
							existingLeaseInfo.count = totalCount
						}
					} else {
						leaseMap[ipString] = currentLeaseInfo
					}
				}
				currentLeaseInfo = nil
			}
		}
	}

	if err = scanner.Err(); err != nil {
		log.Fatalf("scan file error: %v", err)
	}

	logger.Printf("read %v lines", lineNumber)

	return leaseMap
}

func printLeaseMap(leaseMap leaseMap) {
	const formatString = "%-17v%-19v%-6v%-22v%-27v%-27v%-27v"

	logger.Printf(formatString, "IP", "MAC", "Count", "Hostname", "Start Time", "End Time", "Last Transaction Time")
	logger.Printf(strings.Repeat("#", 145))

	ipAddresses := make([]net.IP, 0, len(leaseMap))
	for _, leaseInfo := range leaseMap {
		ipAddresses = append(ipAddresses, leaseInfo.ipAddress)
	}

	sort.Slice(ipAddresses, func(i int, j int) bool {
		return (bytes.Compare(ipAddresses[i], ipAddresses[j]) < 0)
	})

	for _, ipAddress := range ipAddresses {
		ipString := ipAddress.String()
		leaseInfo := leaseMap[ipString]
		logger.Printf(
			formatString,
			ipString, leaseInfo.macAddress.String(),
			leaseInfo.count,
			leaseInfo.hostname,
			leaseInfo.startTime.Format(ouputTimeFormatString),
			leaseInfo.endTime.Format(ouputTimeFormatString),
			leaseInfo.clttTime.Format(ouputTimeFormatString))
	}
}

func main() {
	leaseMap := readLeasesFile()
	printLeaseMap(leaseMap)
}
