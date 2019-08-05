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

	"github.com/boltdb/bolt"
)

var logger = log.New(os.Stdout, "", 0)

const (
	defaultLeasesFile       = "/var/lib/dhcp/dhcpd.leases"
	defaultOuiFile          = "/usr/local/etc/oui.txt"
	ouiDBFile               = "./oui.db"
	ouiDBWriteTXSize        = 1000
	ouiToOrganizationBucket = "ouiToOrganization"
	leaseTimeFormatString   = "2006/01/02 15:04:05;"
	ouputTimeFormatString   = "2006/01/02 15:04:05 -0700"
)

func isHexDigits(s string) bool {
	for _, r := range s {
		if !(('0' <= r && '9' >= r) || ('a' <= r && 'f' >= r) || ('A' <= r && 'F' >= r)) {
			return false
		}
	}
	return true
}

func createOuiDB() {
	ouiFile := defaultOuiFile
	if envValue, ok := os.LookupEnv("OUI_FILE"); ok {
		ouiFile = envValue
	}

	db, err := bolt.Open(ouiDBFile, 0600, nil)
	if err != nil {
		logger.Fatalf("bolt.Open error %v", err)
	}
	defer db.Close()

	logger.Printf("reading %v", ouiFile)
	file, err := os.OpenFile(ouiFile, os.O_RDONLY, os.ModePerm)
	if err != nil {
		logger.Fatalf("Failed to open file %v %s\n", ouiFile, err.Error())
	}
	defer file.Close()

	insertedKeys := make(map[string]bool)
	ouiToOrganizationToInsert := make(map[string]string)

	insertIntoDB := func() {
		logger.Printf("running update tx len(ouiToOrganizationToInsert) = %v", len(ouiToOrganizationToInsert))

		if err := db.Update(func(tx *bolt.Tx) error {

			bucket, err := tx.CreateBucketIfNotExists([]byte(ouiToOrganizationBucket))
			if err != nil {
				return err
			}

			for key, value := range ouiToOrganizationToInsert {
				if err = bucket.Put([]byte(key), []byte(value)); err != nil {
					return err
				}
			}

			return nil
		}); err != nil {
			logger.Fatalf("db.Update error %v", err)
		}

		ouiToOrganizationToInsert = make(map[string]string)
	}

	lineNumber := 0
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		lineNumber++

		line := strings.TrimSpace(scanner.Text())

		if len(line) < 23 {
			continue
		}

		ouiString := line[0:6]
		if !isHexDigits(ouiString) {
			continue
		}

		ouiKeyString := strings.ToLower(ouiString[0:2] + ":" + ouiString[2:4] + ":" + ouiString[4:6])
		organization := line[22:]

		if insertedKeys[ouiKeyString] {
			continue
		}
		insertedKeys[ouiKeyString] = true
		ouiToOrganizationToInsert[ouiKeyString] = organization

		if len(ouiToOrganizationToInsert) >= ouiDBWriteTXSize {
			insertIntoDB()
		}

	}

	if err = scanner.Err(); err != nil {
		logger.Fatalf("scanner error %v", err.Error())
	}

	if len(ouiToOrganizationToInsert) > 0 {
		insertIntoDB()
	}

	logger.Printf("read %v lines from %v", lineNumber, ouiFile)
}

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

	leasesFile := defaultLeasesFile
	if envValue, ok := os.LookupEnv("DHCP_LEASES_FILE"); ok {
		leasesFile = envValue
	}

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
		logger.Fatalf("scan file error: %v", err)
	}

	logger.Printf("read %v lines from %v", lineNumber, leasesFile)

	return leaseMap
}

func printLeaseMap(leaseMap leaseMap) {
	db, err := bolt.Open(ouiDBFile, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		logger.Fatalf("bolt.Open error %v", err)
	}
	defer db.Close()

	const formatString = "%-17v%-19v%-6v%-22v%-27v%-27v%-27v%-24v"

	logger.Printf("")
	logger.Printf(formatString, "IP", "MAC", "Count", "Hostname", "Start Time", "End Time", "Last Transaction Time", "Organization")
	logger.Printf(strings.Repeat("#", 180))

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
		macString := leaseInfo.macAddress.String()
		ouiKeyString := strings.ToLower(macString[0:8])
		organization := "UNKNOWN"

		if err := db.View(func(tx *bolt.Tx) error {
			if value := tx.Bucket([]byte(ouiToOrganizationBucket)).Get([]byte(ouiKeyString)); value != nil {
				organization = string(value)
			}
			return nil
		}); err != nil {
			logger.Fatalf("db.View error %v", err)
		}

		logger.Printf(
			formatString,
			ipString,
			macString,
			leaseInfo.count,
			leaseInfo.hostname,
			leaseInfo.startTime.Format(ouputTimeFormatString),
			leaseInfo.endTime.Format(ouputTimeFormatString),
			leaseInfo.clttTime.Format(ouputTimeFormatString),
			organization)
	}
}

func main() {
	if (len(os.Args) == 2) && (os.Args[1] == "-createdb") {
		logger.Printf("createdb mode")
		createOuiDB()
	} else {
		leaseMap := readLeasesFile()
		printLeaseMap(leaseMap)
	}
}
