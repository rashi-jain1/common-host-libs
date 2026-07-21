// (c) Copyright 2019 Hewlett Packard Enterprise Development LP

package fc

import (
	"fmt"
	"strings"

	"github.com/hpe-storage/common-host-libs/chapi2/model"
	log "github.com/hpe-storage/common-host-libs/logger"
	"github.com/hpe-storage/common-host-libs/util"
)

const (
	fcHostBasePath       = "/sys/class/fc_host"
	fcHostPortNameFormat = "/sys/class/fc_host/host%s/port_name"
	fcHostNodeNameFormat = "/sys/class/fc_host/host%s/node_name"
	fcHostScanPathFormat = "/sys/class/scsi_host/host%s/scan"
	// FcHostLIPNameFormat :
	FcHostLIPNameFormat = "/sys/class/fc_host/host%s/issue_lip"
)

// getHostPort get the host port details for given host number from H:C:T:L of device
func getHostPort(hostNumber string) (hostPort *model.FcHostPort, err error) {
	hostPath := fmt.Sprintf(fcHostPortNameFormat, hostNumber)
	portName, err := util.FileReadFirstLine(hostPath)
	if err != nil {
		log.Errorf("unable to get port WWN for host %s, error %s", hostNumber, err.Error())
		return nil, err
	}
	log.Infof("got port WWN %s for host %s", portName, hostNumber)
	hostPath = fmt.Sprintf(fcHostNodeNameFormat, hostNumber)
	nodeName, err := util.FileReadFirstLine(hostPath)
	if err != nil {
		log.Errorf("unable to get node WWN for host %s, error %s", hostNumber, err.Error())
		return nil, err
	}
	log.Infof("got node WWN %s for host %s", nodeName, hostNumber)
	hostPort = &model.FcHostPort{HostNumber: hostNumber, NodeWwn: strings.TrimPrefix(nodeName, "0x"), PortWwn: strings.TrimPrefix(portName, "0x")}
	return hostPort, nil
}

// getAllFcHostPorts get all the FC host port details on the host
func getAllFcHostPorts() (hostPorts []*model.FcHostPort, err error) {
	log.Infof("getAllFcHostPorts called")
	var hostNumbers []string
	args := []string{"-1", fcHostBasePath}
	exists, _, err := util.FileExists(fcHostBasePath)
	if !exists {
		log.Errorf("no fc adapters found on the host")
		return nil, nil
	}

	out, _, err := util.ExecCommandOutput("ls", args)
	if err != nil {
		log.Errorf("unable to get list of host fc ports, error %s", err.Error())
		return nil, err
	}

	hostNumbers = strings.Split(out, "\n")
	if len(hostNumbers) == 0 {
		log.Errorf("no fc adapters found on the host")
		return nil, nil
	}

	for _, host := range hostNumbers {
		if host != "" {
			hostPort, err := getHostPort(strings.TrimPrefix(host, "host"))
			if err != nil {
				log.Errorf("unable to get details of fc host port %s, error %s", host, err.Error())
				continue
			}
			hostPorts = append(hostPorts, hostPort)
		}
	}
	return hostPorts, nil
}

// getAllFcHostPortWWN get all FC host port WWN's on the host
func getAllFcHostPortWWN() (portWWNs []string, err error) {
	log.Infof("getAllFcHostPortWWN called")
	hostPorts, err := getAllFcHostPorts()
	if err != nil {
		return nil, err
	}
	if len(hostPorts) == 0 {
		return nil, nil
	}
	var inits []string
	for _, hostPort := range hostPorts {
		inits = append(inits, hostPort.PortWwn)
	}
	return inits, nil
}

// getFcHostNumbersForSerial returns SCSI host numbers with active multipathd
// paths to the device identified by serialNumber (WWID).
// Returns empty slice (not error) when no paths are found.
func getFcHostNumbersForSerial(serialNumber string) ([]string, error) {
        if serialNumber == "" {
                return nil, nil
        }

        args := []string{"show", "paths", "format", "%w %d %t %i %o %T %z %s %m"}
        out, _, err := util.ExecCommandOutput("multipathd", args)
        if err != nil {
                return nil, err
        }

        seen := make(map[string]bool)
        for _, line := range strings.Split(out, "\n") {
                if !strings.Contains(line, serialNumber) {
                        continue
                }
                fields := strings.Fields(line)
                if len(fields) < 4 {
                        continue
                }
                parts := strings.Split(fields[3], ":")
                if len(parts) >= 1 && parts[0] != "" {
                        seen[parts[0]] = true
                }
        }

        hosts := make([]string, 0, len(seen))
        for h := range seen {
                hosts = append(hosts, h)
        }
        return hosts, nil
}

// rescanFcHostsForLun rescans only the specified FC host numbers for lunID.
// Avoids triggering ALUA re-evaluation on targets from other arrays.
func rescanFcHostsForLun(hostNumbers []string, lunID string) error {
        for _, hostNum := range hostNumbers {
                fcHostScanPath := fmt.Sprintf(fcHostScanPathFormat, hostNum)
                scanCmd := "- - -"
                if lunID != "" {
                        scanCmd = "- - " + lunID
                }
                err := util.FileWriteString(fcHostScanPath, scanCmd)
                if err != nil {
                        log.Errorf("unable to rescan fc host %s lun %s: %s", hostNum, lunID, err.Error())
                        return err
                }
        }
        return nil
}

// fescanFcTarget rescans host ports for new Fibre Channel devices
func rescanFcTarget(lunID string) (err error) {

	// Get the list of FC hosts to rescan
	fcHosts, err := getAllFcHostPorts()
	if err != nil {
		return err
	}
	for _, fcHost := range fcHosts {
		// perform rescan for all devices
		fcHostScanPath := fmt.Sprintf(fcHostScanPathFormat, fcHost.HostNumber)
		var err error
		if lunID == "" {
			// fallback to the generic host rescan
			err = util.FileWriteString(fcHostScanPath, "- - -")
		} else {
			err = util.FileWriteString(fcHostScanPath, "- - "+lunID)
		}
		if err != nil {
			log.Errorf("unable to rescan for fc devices on host port :%s lun: %s err %s", fcHost.HostNumber, lunID, err.Error())
			return err
		}
	}
	return nil
}

// verifies if the scsi slaves are fc devices are not
func isFibreChannelDevice(slaves []string) bool {
	log.Infof("isFibreChannelDevice called")
	// time.Sleep(time.Duration(1) * time.Second)
	for _, slave := range slaves {
		log.Infof("handling path %s", slave)
		args := []string{"-l", "/sys/block/" + slave}
		out, _, _ := util.ExecCommandOutput("ls", args)
		if strings.Contains(out, "rport") {
			log.Infof("%s is a FC device", slave)
			return true
		}
	}
	return false
}
