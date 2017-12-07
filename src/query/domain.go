package query

import (
	"reflect"
	"sync"
	"time"

	"github.com/miekg/bitradix"
	"github.com/miekg/dns"
	"github.com/petar/GoLLRB/llrb"

	"MyError"
	"utils"
)

const DefaultRadixNetaddr = uint32(1 << 31)
const DefaultRadixNetMask = 1
const DefaultRadixSearchMask = 32

type MuLLRB struct {
	LLRB    *llrb.LLRB
	RWMutex *sync.RWMutex
}

type MubitRadix struct {
	Radix32 *bitradix.Radix32
	RWMutex *sync.RWMutex
}

//For domain name and Region RR
type DomainRRTree MuLLRB

//For domain SOA and NS record
type DomainSOATree MuLLRB

//For domain Region and A/CNAME record
type RegionTree MubitRadix

func NewDomainRegionTree() *RegionTree {
	//	tbitRadix := bitradix.New32()
	return &RegionTree{
		Radix32: bitradix.New32(),
		RWMutex: &sync.RWMutex{},
	}
}

//TODO: redundant data types, need to be redesign
type Domain struct {
	DomainName string
	SOAKey     string // Use this key to search DomainSOANode in DomainSOATree,to find out the NS record
	TTL        uint32
}

type DomainNode struct {
	Domain
	DomainRegionTree *RegionTree
}

func NewDomainNode(d string, soakey string, t uint32) (*DomainNode, *MyError.MyError) {
	if _, ok := dns.IsDomainName(d); !ok {
		return nil, MyError.NewError(MyError.ERROR_PARAM, d+" is not valid domain name")
	}

	return &DomainNode{
		Domain: Domain{
			DomainName: dns.Fqdn(d),
			SOAKey:     soakey,
			TTL:        t,
		},
		DomainRegionTree: NewDomainRegionTree(),
	}, nil
}

//TODO: redundant data types, need to be redesign
// dns.RR && RrType && TTL
type Region struct {
	NetworkAddr uint32
	NetworkMask int
	//	IpStart     uint32
	//	IpEnd       uint32
	RR         []dns.RR
	RrType     uint16
	TTL        uint32
	UpdateTime time.Time
}

func NewRegion(r []dns.RR, networkAddr uint32, networkMask int) (*Region, *MyError.MyError) {
	if len(r) < 1 {
		return nil, MyError.NewError(MyError.ERROR_PARAM, "cap of r ([]dns.RR) can not be less than 1 ")
	}
	//fmt.Println(utils.GetDebugLine(), "NewRegion: ",
	//	" r: ", r, " networkAddr: ", networkAddr, " networkMask: ", networkMask)
	utils.ServerLogger.Debug("NewRegion: r: ", r, " networkAddr: ", networkAddr, " networkMask: ", networkMask)

	dr := &Region{
		NetworkAddr: networkAddr,
		NetworkMask: networkMask,
		//		IpStart:     ipStart,
		//		IpEnd:       ipEnd,
		RR:         r,
		RrType:     r[0].Header().Rrtype,
		TTL:        r[0].Header().Ttl,
		UpdateTime: time.Now(),
	}
	return dr, nil
}

type DomainSOANode struct {
	SOAKey string // store SOA record first field,not the full domain name,but only the "dig -t SOA domain" resoponse
	NS     []*dns.NS
	SOA    *dns.SOA
	//todo: make SOA to be combined structure
}

func NewDomainSOANode(soa *dns.SOA, ns_a []*dns.NS) *DomainSOANode {
	return &DomainSOANode{
		SOAKey: soa.Hdr.Name,
		NS:     ns_a,
		SOA:    soa,
	}
}

type DomainConfig struct {
	DomainName           string
	AuthoritativeServers []string
	Port                 string
	Ttl                  string
}

var once sync.Once

func InitCache() *MyError.MyError {
	once.Do(func() {
		DomainRRCache = &DomainRRTree{
			LLRB:    llrb.New(),
			RWMutex: &sync.RWMutex{},
		}
		DomainSOACache = &DomainSOATree{
			LLRB:    llrb.New(),
			RWMutex: &sync.RWMutex{},
		}
	})
	return nil
}

func (a *DomainNode) Less(b llrb.Item) bool {
	if x, ok := b.(*DomainNode); ok {
		return a.DomainName < x.DomainName
	} else if y, ok := b.(*Domain); ok {
		return a.DomainName < y.DomainName
	}
	panic(MyError.NewError(MyError.ERROR_PARAM, "Param error of b "))
}

func (a *Domain) Less(b llrb.Item) bool {
	if x, ok := b.(*DomainNode); ok {
		return a.DomainName < x.DomainName
	} else if y, ok := b.(*Domain); ok {
		return a.DomainName < y.DomainName
	}
	panic(MyError.NewError(MyError.ERROR_PARAM, "Param error of b "))
}

// 1,Trust d.DomainName is really a DomainName, so, have not use dns.IsDomainName for checking
// Check if d is already in the DomainRRTree,if so,make sure update d.DomainRegionTree = dt.DomainRegionTree
func (DT *DomainRRTree) StoreDomainNodeToCache(d *DomainNode) (bool, *MyError.MyError) {
	dt, err := DT.GetDomainNodeFromCacheWithName(d.DomainName)
	if dt != nil && err == nil {
		//fmt.Println(utils.GetDebugLine(), "DomainRRCache already has DomainNode of d "+d.DomainName)
		utils.ServerLogger.Debug("DomainRRCache already has DomainNode of d %s", d.DomainName)
		d.DomainRegionTree = dt.DomainRegionTree
		return true, nil

	} else if err.ErrorNo != MyError.ERROR_NOTFOUND || err.ErrorNo != MyError.ERROR_TYPE {
		// for not found and type error, we should replace the node
		//fmt.Println(utils.GetDebugLine(), " StoreDomainNodeToCache return error: ", err)
		utils.ServerLogger.Error("StoreDomainNodeToCache return :  %s", err.Error())
		DT.RWMutex.Lock()
		defer DT.RWMutex.Unlock()
		DT.LLRB.ReplaceOrInsert(d)
		//fmt.Println(utils.GetDebugLine(), " Store "+d.DomainName+" into DomainRRCache Done!")
		utils.ServerLogger.Debug(" Store %s into DomainRRCache Done", d.DomainName)
		return true, nil
	}
	return false, err
}

func (DT *DomainRRTree) GetDomainNodeFromCacheWithName(d string) (*DomainNode, *MyError.MyError) {
	if _, ok := dns.IsDomainName(d); ok {
		dn := &Domain{
			DomainName: dns.Fqdn(d),
		}
		return DT.GetDomainNodeFromCache(dn)
	}
	return nil, MyError.NewError(MyError.ERROR_PARAM, "Eorror param: "+reflect.ValueOf(d).String())
}

func (DT *DomainRRTree) GetDomainNodeFromCache(d *Domain) (*DomainNode, *MyError.MyError) {
	DT.RWMutex.RLock()
	defer DT.RWMutex.RUnlock()
	dr := DT.LLRB.Get(d)
	if dr != nil {
		if drr, ok := dr.(*DomainNode); ok {
			return drr, nil
		} else {
			return nil, MyError.NewError(MyError.ERROR_TYPE, "Got error result because of the type of return value is "+reflect.TypeOf(dr).String())
		}
	} else {
		return nil, MyError.NewError(MyError.ERROR_NOTFOUND, "Not found DomainNode from DomainRRCache for param: "+reflect.ValueOf(d.DomainName).String())
	}
	return nil, MyError.NewError(MyError.ERROR_UNKNOWN, "SearchDomainNode got param: "+reflect.ValueOf(d).String())
}

func (DT *DomainRRTree) UpdateDomainNode(d *DomainNode) (bool, *MyError.MyError) {
	if _, ok := dns.IsDomainName(d.DomainName); ok {
		if dt, err := DT.GetDomainNodeFromCache(&d.Domain); dt != nil && err == nil {
			d.DomainRegionTree = dt.DomainRegionTree
			DT.RWMutex.Lock()
			r := DT.LLRB.ReplaceOrInsert(d)
			DT.RWMutex.Unlock()
			if r != nil {
				return true, nil

			} else {
				//Exception:see source code of "LLRB.ReplaceOrInsert"
				return true, MyError.NewError(MyError.ERROR_UNKNOWN, "Update error, but inserted")
			}
		} else {
			return false, MyError.NewError(MyError.ERROR_NOTFOUND, "DomainRRTree does not has "+reflect.ValueOf(d).String()+" or it has "+reflect.ValueOf(dt).String())
		}
	} else {
		return false, MyError.NewError(MyError.ERROR_PARAM, " Param d "+reflect.ValueOf(d).String()+" is not valid Domain instance")
	}
	return false, MyError.NewError(MyError.ERROR_UNKNOWN, "UpdateDomainNode return unknown error")
}

//Use interface{} as param ,  may refact other func as this
//TODO: this func has not been completed,don't use it
func (DT *DomainRRTree) DelDomainNode(d *Domain) (bool, *MyError.MyError) {
	DT.RWMutex.Lock()
	r := DT.LLRB.Delete(d)
	DT.RWMutex.Unlock()
	//fmt.Println(utils.GetDebugLine(), "Delete "+d.DomainName+" from DomainRRCache "+reflect.ValueOf(r).String())
	utils.ServerLogger.Debug("Delete %s from DomainRRCache %s ", d.DomainName, reflect.ValueOf(r).String())
	return true, nil
}

func (DS *DomainSOANode) Less(b llrb.Item) bool {
	if x, ok := b.(*DomainSOANode); ok {
		return DS.SOAKey < x.SOAKey
	}
	panic(MyError.NewError(MyError.ERROR_PARAM, "Param b "+reflect.ValueOf(b).String()+" is not valid DomainSOANode or String"))
}

func (ST *DomainSOATree) StoreDomainSOANodeToCache(dsn *DomainSOANode) (bool, *MyError.MyError) {
	dt, err := ST.GetDomainSOANodeFromCache(dsn)
	//	fmt.Println(dt,err)
	if dt != nil && err == nil {
		//fmt.Println(utils.GetDebugLine(), "DomainSOACache already has DomainSOANode of dsn "+dsn.SOAKey)
		utils.ServerLogger.Debug("DomainSOACache already has DomainSOANode of dsn %s", dsn.SOAKey, dsn)
		return true, nil
	} else if err.ErrorNo != MyError.ERROR_NOTFOUND || err.ErrorNo != MyError.ERROR_TYPE {
		// for not found and type error, we should replace the node
		//fmt.Println(utils.GetDebugLine(), "StoreDomainSOANodeToCache: ", err)
		utils.ServerLogger.Error("StoreDomainSOANodeToCache:  %s", err.Error())
		ST.RWMutex.Lock()
		ST.LLRB.ReplaceOrInsert(dsn)
		ST.RWMutex.Unlock()
		//fmt.Println(utils.GetDebugLine(), "StoreDomainSOANodeToCache : Store "+dsn.SOAKey+" into DomainSOACache Done!")
		utils.ServerLogger.Debug("StoreDomainSOANodeToCache : Store %s into DomainSOACache Done", dsn.SOAKey, dsn)
		return true, nil
	}
	return false, err
}

func (ST *DomainSOATree) GetDomainSOANodeFromCache(dsn *DomainSOANode) (*DomainSOANode, *MyError.MyError) {
	ST.RWMutex.RLock()
	defer ST.RWMutex.RUnlock()
	if dt := ST.LLRB.Get(dsn); dt != nil {
		if dsn_r, ok := dt.(*DomainSOANode); ok {
			return dsn_r, nil
		} else {
			return nil, MyError.NewError(MyError.ERROR_TYPE, "ERROR_TYPE")
		}
	} else {
		return nil, MyError.NewError(MyError.ERROR_NOTFOUND, "Not found soa record from DomainSOACache via domainname "+dsn.SOAKey)
	}
	return nil, MyError.NewError(MyError.ERROR_UNKNOWN, "Unknown Error!")
}

func (ST *DomainSOATree) GetDomainSOANodeFromCacheWithDomainName(d string) (*DomainSOANode, *MyError.MyError) {
	ds := &DomainSOANode{
		SOAKey: dns.Fqdn(d),
	}
	return ST.GetDomainSOANodeFromCache(ds)
}

//func (ST *DomainSOATree) UpdateDomainSOANode(ds *DomainSOANode) *MyError.MyError {
//
//	ST.LLRB.ReplaceOrInsert(ds)
//	return nil
//}

//todo:have not completed
func (ST *DomainSOATree) DelDomainSOANode(ds *DomainSOANode) *MyError.MyError {
	ST.RWMutex.Lock()
	ST.LLRB.Delete(ds)
	ST.RWMutex.Unlock()
	return nil
}

func (a *DomainNode) InitRegionTree() (bool, *MyError.MyError) {
	if a.DomainRegionTree == nil {
		a.DomainRegionTree = NewDomainRegionTree()
	}
	return true, nil
}

func (RT *RegionTree) GetRegionFromCache(r *Region) (*Region, *MyError.MyError) {
	return RT.GetRegionFromCacheWithAddr(r.NetworkAddr, r.NetworkMask)
}

func (RT *RegionTree) GetRegionFromCacheWithAddr(addr uint32, mask int) (*Region, *MyError.MyError) {
	RT.RWMutex.RLock()
	defer RT.RWMutex.RUnlock()
	if r := RT.Radix32.Find(addr, mask); r != nil && r.Value != nil {
		//fmt.Println(utils.GetDebugLine(), "GetRegionFromCacheWithAddr : ", r, addr, reflect.TypeOf(addr), mask, reflect.TypeOf(mask))
		utils.ServerLogger.Debug("GetRegionFromCacheWithAddr: ", r, addr, reflect.TypeOf(addr), mask, reflect.TypeOf(mask))
		if rr, ok := r.Value.(*Region); ok {
			return rr, nil
		} else {
			return nil, MyError.NewError(MyError.ERROR_NOTVALID, "Found result but not valid,need check !")
		}
	} else if addr != DefaultRadixNetaddr && mask != DefaultRadixNetMask {
		return RT.GetRegionFromCacheWithAddr(DefaultRadixNetaddr, DefaultRadixNetMask)
	}
	return nil, MyError.NewError(MyError.ERROR_NOTFOUND, "Not found search region "+string(addr)+":"+string(mask))
}

//Todo: need check wheather this region.NetworkAddr is in Cache Radix tree,but region.NetworkMask is not
func CheckRegionFromCache(r *Region) bool {
	if len(r.RR) < 1 {
		return false
	}

	return true
}

func (RT *RegionTree) AddRegionToCache(r *Region) bool {
	if ok := CheckRegionFromCache(r); !ok {
		//Todo: add split region logic
	}
	RT.RWMutex.Lock()
	defer RT.RWMutex.Unlock()
	RT.Radix32.Insert(r.NetworkAddr, r.NetworkMask, r)
	//fmt.Println(utils.GetDebugLine(), "AddRegionToCache : ",
	//	" NetworkAddr: ", r.NetworkAddr, " NetworkMask: ", r.NetworkMask, " RR: ", r.RR)
	return true
}

func (RT *RegionTree) UpdateRegionToCache(r *Region) bool {
	if rnode, e := RT.GetRegionFromCache(r); e == nil && rnode != nil {
		RT.RWMutex.Lock()
		defer RT.RWMutex.Unlock()
		RT.Radix32.Remove(r.NetworkAddr, r.NetworkMask)
		RT.Radix32.Insert(r.NetworkAddr, r.NetworkMask, r)
	} else {
		RT.AddRegionToCache(r)
	}
	return true
}

func (RT *RegionTree) DelRegionFromCache(r *Region) (bool, *MyError.MyError) {
	if rnode, e := RT.GetRegionFromCache(r); rnode != nil && e == nil {
		RT.RWMutex.Lock()
		RT.Radix32.Remove(r.NetworkAddr, r.NetworkMask)
		RT.RWMutex.Unlock()
		//fmt.Println(utils.GetDebugLine(), "Remove Region from RegionCache "+string(r.NetworkAddr)+":"+string(r.NetworkMask))
		utils.ServerLogger.Debug("Remove Region from RegionCache %s : %s", string(r.NetworkAddr), string(r.NetworkMask))
		return true, nil
	} else {
		return true, MyError.NewError(MyError.ERROR_NOTFOUND, "Not found Region from RegionCache")
	}

}

func (RT *RegionTree) TraverseRegionTree() {
	RT.Radix32.Do(func(r1 *bitradix.Radix32, i int) {
		//fmt.Println(utils.GetDebugLine(), r1.Key(),
		//	r1.Value,
		//	r1.Bits(),
		//	r1.Leaf(), i)
		utils.ServerLogger.Debug("TraverseRegionTree: ", r1.Value, r1.Bits(), r1.Leaf(), i)
	})
}
