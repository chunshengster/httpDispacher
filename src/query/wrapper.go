package query

import (
	"net"
	"reflect"
	"strconv"
	"time"

	"github.com/chunshengster/httpDispacher/src/query"
	"github.com/miekg/dns"

	"MyError"
	"config"
	"utils"
)

const CNAME_CHAIN_LENGTH = 10

func GetSOARecord(d string) (*DomainSOANode, *MyError.MyError) {

	var soa *DomainSOANode

	dn, e := DomainRRCache.GetDomainNodeFromCacheWithName(d)
	if e == nil && dn != nil {
		dsoa_key := dn.SOAKey
		soa, e = DomainSOACache.GetDomainSOANodeFromCacheWithDomainName(dsoa_key)
		utils.ServerLogger.Debug("GetDomainSOANodeFromCacheWithDomainName: key: %s soa %v", dsoa_key, soa)
		if e == nil && soa != nil {
			return soa, nil
		} else {
			// error == nil bug soa record also == nil
			utils.ServerLogger.Critical("GetSOARecord->GetDomainSOANodeFromCacheWithDomainName unknown error")
		}
	}
	soa_t, ns, e := QuerySOA(d)
	// Need to store DomainSOANode and DomainNOde both
	if e == nil && soa_t != nil && ns != nil {
		soa = NewDomainSOANode(soa_t, ns)
		go func(d string, soa *DomainSOANode) {
			//TODO: get StoreDomainSOANode return values
			_, e := DomainSOACache.StoreDomainSOANodeToCache(soa)
			if e != nil {
				utils.ServerLogger.Error("DomainSOACache.StoreDomainSOANodeToCache return error :",
					e, " param :", soa)
			} else {
				utils.ServerLogger.Debug("DomainSOACache.StoreDomainSOANodeToCache return OK", soa)
			}
			rrnode, _ := NewDomainNode(d, soa.SOAKey, soa.SOA.Expire)
			_, e = DomainRRCache.StoreDomainNodeToCache(rrnode)
			if e != nil {
				utils.ServerLogger.Error("DomainRRCache.StoreDomainNodeToCache return error :",
					e, " param :", rrnode)
			} else {
				utils.ServerLogger.Debug("DomainRRCache.StoreDomainNodeToCache return ok", rrnode)
			}
		}(d, soa)

		return soa, nil
	}
	// QuerySOA fail
	return nil, MyError.NewError(MyError.ERROR_UNKNOWN, "Finally GetSOARecord failed")
}

func GetARecord(d string, srcIP string) (bool, []dns.RR, *MyError.MyError) {
	var Regiontree *RegionTree
	var bigloopflag bool = false // big loop flag
	var c = 0                    //big loop count

	//Can't loop for CNAME chain than bigger than CNAME_CHAIN_LENGTH
	for dst := d; (bigloopflag == false) && (c < CNAME_CHAIN_LENGTH); c++ {
		utils.ServerLogger.Debug("Trying GetARecord : %s srcIP: %s", dst, srcIP)

		dn, RR, e := GetAFromCache(dst, srcIP)
		utils.ServerLogger.Debug("GetAFromCache return: ", dn, RR, e)
		if e == nil {
			// All is right and especilly RR is A record
			return true, RR, nil
		} else {
			//Return Cname record
			if (e.ErrorNo == MyError.ERROR_CNAME) && (dn != nil) && (RR != nil) {
				if dst_cname, ok := RR[0].(*dns.CNAME); ok {
					dst = dst_cname.Target
					continue
				} else {
					utils.ServerLogger.Error("dn:", dn, "RR:", RR, "e:", e)
				}
			} else {
				// hava domain node ,but region node is nil,need queryA
				utils.ServerLogger.Error("error get dn:", dn, "RR:", RR, "e:", e, "need query A from dns/mysql backend")
			}
		}

		utils.ServerLogger.Info("Need to get dst from backend: ", dst, " srcIP: ", srcIP)
		//fmt.Println(utils.GetDebugLine(), "++++++++++++++++++++++++++++++++++++++++++++++")
		if config.IsLocalMysqlBackend(dst) {
			//fmt.Println(utils.GetDebugLine(), "**********************************************")
			//need pass dn to GetAFromMySQLBackend, to fill th dn.RegionTree node
			ok, RR, rtype, ee := GetAFromMySQLBackend(dst, srcIP, Regiontree)
			//fmt.Println(utils.GetDebugLine(), " Debug: GetAFromMySQLBackend: return ", ok,
			//	" RR: ", RR, " error: ", ee)
			utils.ServerLogger.Debug("GetAFromMySQLBackend: return ", ok, RR, rtype, ee)
			if !ok {
				//fmt.Println(utils.GetDebugLine(), "Error: GetAFromMySQL error : ", ee)
				utils.ServerLogger.Error("Error: GetAFromMySQL error : ", ee)
			} else if rtype == dns.TypeA {
				//fmt.Println(utils.GetDebugLine(), "Info: Got A record, : ", RR)
				utils.ServerLogger.Debug("Got A record: ", RR)
				return true, RR, nil
			} else if rtype == dns.TypeCNAME {
				//fmt.Println(utils.GetDebugLine(), "Info: Got CNAME record, ReGet dst : ", dst, RR)
				utils.ServerLogger.Debug("Got CNAME record, ReGet dst: ", dst, RR)
				dst = RR[0].(*dns.CNAME).Target
				continue
			}
		} else {
			//fmt.Println(utils.GetDebugLine(), "Info: Got dst: ", dst, " srcIP: ", srcIP, " soa.NS: ", soa.NS)

			ok, rr_i, rtype, ee := GetAFromDNSBackend(dst, srcIP)
			//go func() {
			//	AddAToCache()
			//}()
			if ok && rtype == dns.TypeA {
				return true, rr_i, nil
			} else if ok && rtype == dns.TypeCNAME {
				dst = rr_i[0].(*dns.CNAME).Target
				continue
			} else if !ok && rr_i == nil && ee != nil && ee.ErrorNo == MyError.ERROR_NORESULT {
				continue
			} else {
				return false, nil, MyError.NewError(MyError.ERROR_UNKNOWN, "Unknown error")
			}
		}
	}
	//fmt.Println(utils.GetDebugLine(), "GetARecord: ", Regiontree)
	return false, nil, MyError.NewError(MyError.ERROR_UNKNOWN, "Unknown error")
}

func GetAFromCache(dst, srcIP string) (*DomainNode, []dns.RR, *MyError.MyError) {
	dn, e := DomainRRCache.GetDomainNodeFromCacheWithName(dst)
	if e == nil && dn != nil && dn.DomainRegionTree != nil {
		//Get DomainNode succ,
		r, e := dn.DomainRegionTree.GetRegionFromCacheWithAddr(
			utils.Ip4ToInt32(net.ParseIP(srcIP)), DefaultRadixSearchMask)
		if e == nil && len(r.RR) > 0 {
			if r.RrType == dns.TypeA {
				utils.ServerLogger.Debug("GetAFromCache: Goooot A ", dst, srcIP, r.RR)
				return dn, r.RR, nil
			} else if r.RrType == dns.TypeCNAME {
				utils.ServerLogger.Debug("GetAFromCache: Goooot CNAME ", dst, srcIP, r.RR)
				return dn, r.RR, MyError.NewError(MyError.ERROR_CNAME,
					"Get CNAME From,Requery A for "+r.RR[0].(*dns.CNAME).Target)
			}
		}

		return dn, nil, MyError.NewError(MyError.ERROR_NOTFOUND,
			"Not found R in cache, dst :"+dst+" srcIP "+srcIP)
		// return
	} else if e == nil && dn != nil && dn.DomainRegionTree == nil {
		// Get domainNode in cache tree,but no RR in region tree,need query with NS
		// if RegionTree is nil, init RegionTree First
		ok, e := dn.InitRegionTree()
		if e != nil {
			utils.ServerLogger.Error("InitRegionTree fail %s", e.Error())
		}
		//
		//fmt.Println("RegionTree is nil ,Init it: "+reflect.ValueOf(ok).String(), e)
		utils.ServerLogger.Debug("RegionTree is nil ,Init it: %s ", reflect.ValueOf(ok).String())
		return dn, nil, MyError.NewError(MyError.ERROR_NORESULT,
			"Get domainNode in cache tree,but no RR in region tree,need query with NS, dst : "+dst+" srcIP "+srcIP)
	} else {
		// e != nil
		// RegionTree is not nil
		if e != nil {
			if e.ErrorNo != MyError.ERROR_NOTFOUND {
				//fmt.Println("Found unexpected error, need return !")
				utils.ServerLogger.Info("Not found, need return :", "error :", e, "dn:", dn)
				//os.Exit(2)
			} else {
				utils.ServerLogger.Critical("Found unexpected error, need return !", "error :", e, "dn:", dn)
			}
		}
		return nil, nil, e
	}
	return nil, nil, MyError.NewError(MyError.ERROR_UNKNOWN, "Unknown error!")
}

func GetAFromMySQLBackend(dst, srcIP string, regionTree *RegionTree) (bool, []dns.RR, uint16, *MyError.MyError) {
	domainId, e := RRMySQL.GetDomainIDFromMySQL(dst)
	if e != nil {
		//todo:
		//fmt.Println(utils.GetDebugLine(), "Error, GetDomainIDFromMySQL:", e)
		return false, nil, uint16(0), e
	}
	region, ee := RRMySQL.GetRegionWithIPFromMySQL(utils.Ip4ToInt32(utils.StrToIP(srcIP)))
	if ee != nil {
		//fmt.Println(utils.GetDebugLine(), "Error GetRegionWithIPFromMySQL:", ee)
		return false, nil, uint16(0), MyError.NewError(ee.ErrorNo, "GetRegionWithIPFromMySQL return "+ee.Error())
	}
	RR, eee := RRMySQL.GetRRFromMySQL(uint32(domainId), region.IdRegion)
	if eee != nil && eee.ErrorNo == MyError.ERROR_NORESULT {
		//fmt.Println(utils.GetDebugLine(), "Error GetRRFromMySQL with DomainID:", domainId,
		//	"RegionID:", region.IdRegion, eee)
		//fmt.Println(utils.GetDebugLine(), "Try to GetRRFromMySQL with Default Region")
		utils.ServerLogger.Debug("Try to GetRRFromMySQL with Default Region")
		RR, eee = RRMySQL.GetRRFromMySQL(uint32(domainId), uint32(0))
		if eee != nil {
			//fmt.Println(utils.GetDebugLine(), "Error GetRRFromMySQL with DomainID:", domainId,
			//	"RegionID:", 0, eee)
			return false, nil, uint16(0), MyError.NewError(eee.ErrorNo, "Error GetRRFromMySQL with DomainID:"+strconv.Itoa(domainId)+eee.Error())
		}
	} else if eee != nil {
		utils.ServerLogger.Error(eee.Error())
		return false, nil, uint16(0), eee
	}
	//fmt.Println(utils.GetDebugLine(), "GetRRFromMySQL Succ!:", RR)
	utils.ServerLogger.Debug("GetRRFromMySQL Succ!: ", RR)
	var R []dns.RR
	var rtype uint16
	var reE *MyError.MyError
	hdr := dns.RR_Header{
		Name:   dst,
		Class:  RR.RR.Class,
		Rrtype: RR.RR.RrType,
		Ttl:    RR.RR.Ttl,
	}

	//fmt.Println(utils.GetDebugLine(), mr.RR)
	if RR.RR.RrType == dns.TypeA {
		for _, mr := range RR.RR.Target {
			rh := &dns.A{
				Hdr: hdr,
				A:   utils.StrToIP(mr),
			}
			R = append(R, dns.RR(rh))
		}
		rtype = dns.TypeA
		//	fmt.Println(utils.GetDebugLine(), "Get A RR from MySQL, requery dst:", dst)
	} else if RR.RR.RrType == dns.TypeCNAME {
		for _, mr := range RR.RR.Target {
			rh := &dns.CNAME{
				Hdr:    hdr,
				Target: mr,
			}
			R = append(R, dns.RR(rh))
		}
		rtype = dns.TypeCNAME
		//fmt.Println(utils.GetDebugLine(), "Get CNAME RR from MySQL, requery dst:", dst)
		reE = MyError.NewError(MyError.ERROR_NOTVALID,
			"Got CNAME result for dst : "+dst+" with srcIP : "+srcIP)
	}

	if len(R) > 0 {
		//Add timer for auto refrech the RegionCache
		go func(dst, srcIP string, r dns.RR, regionTree *RegionTree) {
			//fmt.Println(utils.GetDebugLine(), " Refresh record after ", r.Header().Ttl-5,
			//	" Second, dst: ", dst, " srcIP: ", srcIP, "add timer ")
			time.AfterFunc(time.Duration(r.Header().Ttl-5)*time.Second,
				func() { GetAFromMySQLBackend(dst, srcIP, regionTree) })
		}(dst, srcIP, R[0], regionTree)

		go func(regionTree *RegionTree, R []dns.RR, srcIP string) {
			//fmt.Println(utils.GetDebugLine(), "GetAFromMySQLBackend: ", e)

			startIP, endIP := region.Region.StarIP, region.Region.EndIP
			cidrmask := utils.GetCIDRMaskWithUint32Range(startIP, endIP)

			//fmt.Println(utils.GetDebugLine(), " GetRegionWithIPFromMySQL with srcIP: ",
			//	srcIP, " StartIP : ", startIP, "==", utils.Int32ToIP4(startIP).String(),
			//	" EndIP: ", endIP, "==", utils.Int32ToIP4(endIP).String(), " cidrmask : ", cidrmask)
			//				netaddr, mask := DefaultNetaddr, DefaultMask
			r, _ := NewRegion(R, startIP, cidrmask)
			regionTree.AddRegionToCache(r)
			//fmt.Println(utils.GetDebugLine(), "GetAFromMySQLBackend: ", r)
			//				fmt.Println(regionTree.GetRegionFromCacheWithAddr(startIP, cidrmask))
		}(regionTree, R, srcIP)
		return true, R, rtype, reE
	}
	return false, nil, uint16(0), MyError.NewError(MyError.ERROR_UNKNOWN, utils.GetDebugLine()+"Unknown Error ")

}

func GetAFromDNSBackend(
	dst, srcIP string) (bool, []dns.RR, uint16, *MyError.MyError) {

	var reE *MyError.MyError = nil
	var rtype uint16
	soa, e := GetSOARecord(dst)
	utils.ServerLogger.Debug("GetSOARecord return: ", soa, " error: ", e)
	if e != nil || len(soa.NS) <= 0 {
		//GetSOA failed , need log and return
		utils.ServerLogger.Error("GetSOARecord error: %s", e.Error())
		return false, nil, dns.TypeNone, MyError.NewError(MyError.ERROR_UNKNOWN,
			"GetARecord func GetSOARecord failed: "+dst)
	}

	var ns_a []string
	//todo: is that soa.NS may nil ?
	for _, x := range soa.NS {
		ns_a = append(ns_a, x.Ns)
	}

	rr, edns_h, edns, e := QueryA(dst, srcIP, ns_a, query.NS_SERVER_PORT)
	//todo: ends_h ends need to be parsed and returned!
	utils.QueryLogger.Info("QueryA(): dst:", dst, "srcIP:", srcIP, "ns_a:", ns_a, " returned rr:", rr, "edns_h:", edns_h,
		"edns:", edns, "e:", e)
	if e == nil && rr != nil {
		var rr_i []dns.RR
		//todo:if you add both "A" and "CNAME" record to a domain name,this should be wrong!
		if a, ok := ParseA(rr, dst); ok {
			//rr is A record
			utils.ServerLogger.Debug("GetAFromDNSBackend : typeA record: ", a, " dns.TypeA: ", ok)
			for _, i := range a {
				rr_i = append(rr_i, dns.RR(i))
			}
			//if A ,need parse edns client subnet
			//			return true,rr_i,nil
			rtype = dns.TypeA
		} else if b, ok := ParseCNAME(rr, dst); ok {
			//rr is CNAME record
			//fmt.Println(utils.GetDebugLine(), "GetAFromDNSBackend: typeCNAME record: ", b, " dns.TypeCNAME: ", ok)
			utils.ServerLogger.Debug("GetAFromDNSBackend: typeCNAME record: ", b, " dns.TypeCNAME: ", ok)
			//todo: if you add more than one "CNAME" record to a domain name ,this should be wrong,only the first one will be used!
			//dst = b[0].Target
			for _, i := range b {
				rr_i = append(rr_i, dns.RR(i))
			}
			rtype = dns.TypeCNAME
			reE = MyError.NewError(MyError.ERROR_NOTVALID,
				"Got CNAME result for dst : "+dst+" with srcIP : "+srcIP)
			//if CNAME need parse edns client subnet
		} else {
			//error return and retry
			//fmt.Println(utils.GetDebugLine(), "GetAFromDNSBackend: ", rr)
			utils.ServerLogger.Debug("GetAFromDNSBackend: ", rr)
			return false, nil, dns.TypeNone, MyError.NewError(MyError.ERROR_NORESULT,
				"Got error result, need retry for dst : "+dst+" with srcIP : "+srcIP)
		}
		utils.ServerLogger.Debug("Add A record to Region Cache: dst:", dst, "srcIP:", srcIP,
			"rr_i:", rr_i, "ends_h", edns_h, "edns:", edns)
		go AddAToRegionCache(dst, srcIP, rr_i, edns_h, edns)

		return true, rr_i, rtype, reE
	}
	return false, nil, dns.TypeNone, MyError.NewError(MyError.ERROR_UNKNOWN, utils.GetDebugLine()+"Unknown error")
}

func AddAToRegionCache(dst string, srcIP string, R []dns.RR, edns_h *dns.RR_Header, edns *dns.EDNS0_SUBNET) {

	var dn *DomainNode
	var domainNodeExist bool = false
	var retry = 0
	var e *MyError.MyError
	for domainNodeExist = false; (domainNodeExist == false) && (retry < 5); {
		// wait for goroutine 'StoreDomainNodeToCache' in GetSOARecord to be finished
		dn, e = DomainRRCache.GetDomainNodeFromCacheWithName(dst)
		if e != nil {
			// here ,may be nil
			// error! need return
			//fmt.Println(utils.GetDebugLine(),
			//	" GetARecord : have not got cache GetDomainNodeFromCacheWithName, need waite ", e)
			utils.ServerLogger.Error("GetARecord : have not got cache GetDomainNodeFromCacheWithName, need waite %s", e.Error())
			time.Sleep(1 * time.Second)
			if e.ErrorNo == MyError.ERROR_NOTFOUND {
				retry++
			} else {
				utils.ServerLogger.Critical("GetARecord error %s", e.Error())
				//fmt.Println(utils.GetDebugLine(), e)
				// todo: need to log error
			}

		} else {
			domainNodeExist = true
			//fmt.Println(utils.GetDebugLine(), "GetARecord: ", dn)
			utils.ServerLogger.Debug("GetARecord dn: %s", dn.Domain.DomainName, dn.DomainRegionTree)
		}
	}
	if domainNodeExist == false && retry >= 5 {
		utils.ServerLogger.Warning("DomainRRCache.GetDomainNodeFromCacheWithName(dst) dst:", dst, " retry for ", retry,
			" times,but no result!")
	} else {
		//dn.InitRegionTree()
		utils.ServerLogger.Debug("Got dn :", dn)
		regiontree := dn.DomainRegionTree

		////todo: Need to be combined with the go func within GetAFromMySQLBackend
		//var startIP, endIP uint32

		//if config.RC.MySQLEnabled /** && config.RC.MySQLRegionEnabled **/ {
		//	region, ee := RRMySQL.GetRegionWithIPFromMySQL(utils.Ip4ToInt32(utils.StrToIP(srcIP)))
		//	if ee != nil {
		//		//fmt.Println(utils.GetDebugLine(), "Error GetRegionWithIPFromMySQL:", ee)
		//		utils.ServerLogger.Error("Error GetRegionWithIPFromMySQL: %s", ee.Error())
		//	} else {
		//		startIP, endIP = region.Region.StarIP, region.Region.EndIP
		//		//fmt.Println(utils.GetDebugLine(), region.Region, startIP, endIP)
		//	}
		//
		//} else {
		//	//				startIP, endIP = iplookup.GetIpinfoStartEndWithIPString(srcIP)
		//}
		//cidrmask := utils.GetCIDRMaskWithUint32Range(startIP, endIP)
		//fmt.Println(utils.GetDebugLine(), "Search client region info with srcIP: ",
		//	srcIP, " StartIP : ", startIP, "==", utils.Int32ToIP4(startIP).String(),
		//	" EndIP: ", endIP, "==", utils.Int32ToIP4(endIP).String(), " cidrmask : ", cidrmask)
		if edns != nil {
			var ipnet *net.IPNet

			ipnet, e := utils.ParseEdnsIPNet(edns.Address, edns.SourceScope, edns.Family)
			if e != nil {
				utils.ServerLogger.Error("utils.ParseEdnsIPNet error:", edns)
			}
			netaddr, mask := utils.IpNetToInt32(ipnet)
			//fmt.Println(utils.GetDebugLine(), "Got Edns client subnet from ecs query, netaddr : ", netaddr,
			//	" mask : ", mask)
			utils.ServerLogger.Debug("Got Edns client subnet from ecs query, netaddr : ", netaddr, " mask : ", mask)
			//if (netaddr != startIP) || (mask != cidrmask) {
			//	//fmt.Println(utils.GetDebugLine(), "iplookup data dose not match edns query result , netaddr : ",
			//	//	netaddr, "<->", startIP, " mask: ", mask, "<->", cidrmask)
			//	utils.ServerLogger.Debug("iplookup data dose not match edns query result , netaddr : ", netaddr, "<->", startIP, " mask: ", mask, "<->", cidrmask)
			//}
			//// if there is no region info in region table of mysql db or no info in ipdb
			//if cidrmask <= 0 || startIP <= 0 {
			//	startIP = netaddr
			//	cidrmask = mask
			//}
			r, _ := NewRegion(R, netaddr, mask)

			// Parse edns client subnet
			utils.ServerLogger.Debug("GetAFromDNSBackend: ", " edns_h: ", edns_h, " edns: ", edns)

			regiontree.AddRegionToCache(r)

		} else {
			//todo: get StartIP/EndIP from iplookup module

			//				netaddr, mask := DefaultNetaddr, DefaultMask
			r, _ := NewRegion(R, DefaultRadixNetaddr, DefaultRadixNetMask)
			//todo: modify to go func,so you can cathe the result
			regiontree.AddRegionToCache(r)
			//fmt.Println(utils.GetDebugLine(), "GetAFromDNSBackend: AddRegionToCache: ", r)
			//fmt.Println(regionTree.GetRegionFromCacheWithAddr(startIP, cidrmask))
		}
		//todo: modify to go func,so you can cathe the result
		//Add timer for auto refrech the RegionCache
		go func(dst, srcIP string, r dns.RR, regionTree *RegionTree) {
			//fmt.Println(utils.GetDebugLine(), " Refresh record after ", r.Header().Ttl-5,
			//	" Second, dst: ", dst, " srcIP: ", srcIP, " ns_a: ", ns_a, "add timer ")
			time.AfterFunc((time.Duration(r.Header().Ttl-5))*time.Second,
				func() {
					GetAFromDNSBackend(dst, srcIP)
					//todo:add timer for update region cache
					utils.QueryLogger.Info("Need to refresh for domain:", dst, " srcIP: ", srcIP)
				})
		}(dst, srcIP, R[0], regiontree)
	}

}

//func temp()  {
//

//}
