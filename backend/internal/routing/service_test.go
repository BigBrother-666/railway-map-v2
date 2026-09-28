package routing

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/model"
)

// fakeGeoSource 是测试用的 GeoSource：geojson 固定不变（版本号固定），lines/systems 为空。
type fakeGeoSource struct {
	geojson []byte
	version string
}

func (f *fakeGeoSource) Geojson() ([]byte, string) { return f.geojson, f.version }
func (f *fakeGeoSource) Lines() []byte             { return nil }
func (f *fakeGeoSource) Systems() []byte           { return nil }

func newTestService(t *testing.T, geojsonBytes []byte, version string) *Service {
	t.Helper()
	return newTestServiceWithConfig(t, geojsonBytes, version, config.RouteConfig{
		MaxDistanceResults: 5, MaxPriceResults: 5,
		SearchWeightDistance: 0.5, SearchWeightPrice: 0.5,
		MinDirectResults: 1, MaxTransferResults: 3, TransferMinImprovement: 0.2,
	})
}

func newTestServiceWithConfig(t *testing.T, geojsonBytes []byte, version string, cfg config.RouteConfig) *Service {
	t.Helper()
	source := &fakeGeoSource{geojson: geojsonBytes, version: version}
	return NewService(source, func() config.RouteConfig { return cfg }, 0.2)
}

func geojsonFixture(t *testing.T) []byte {
	t.Helper()
	fcVal := fc([]model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nB", "station", "B", 10, 0),
		line("e.L.nA__nB", "nA", "nB", "L", 10, nil),
	})
	b, err := json.Marshal(fcVal)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// geojsonDisconnectedFixture 构造两个完全不连通的车站（无任何边），用于测试"确定无路线"场景。
func geojsonDisconnectedFixture(t *testing.T) []byte {
	t.Helper()
	fcVal := fc([]model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nB", "station", "B", 100, 100),
	})
	b, err := json.Marshal(fcVal)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestService_QueryCachesResultAcrossCalls(t *testing.T) {
	svc := newTestService(t, geojsonFixture(t), "v1")

	var computeCount atomic.Int32
	testComputeHook = func() { computeCount.Add(1) }
	defer func() { testComputeHook = nil }()

	first, err := svc.Query(context.Background(), "A", "B")
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	second, err := svc.Query(context.Background(), "A", "B")
	if err != nil {
		t.Fatalf("second query: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("results differ in length: %d vs %d", len(first), len(second))
	}
	if computeCount.Load() != 1 {
		t.Errorf("compute ran %d times, want 1 (second call should hit cache)", computeCount.Load())
	}
}

func TestService_QueryReturnsQuicklyWhenCallerCtxCancelled(t *testing.T) {
	svc := newTestService(t, geojsonFixture(t), "v1")

	release := make(chan struct{})
	done := make(chan struct{})
	testComputeHook = func() { <-release } // 让计算阻塞，直到测试放行
	// testComputeDoneHook 在 ComputeCandidates 真正返回之后才触发，而不是 testComputeHook 那个
	// "计算刚开始"的同步点——用它才能确认后台 goroutine 已经跑完，而不是刚被放行、还没真正执行完
	// ComputeCandidates 就以为它结束了（那样后面清理全局 hook 时，这个 goroutine 仍可能在跑，
	// 与下一个测试写入同一个全局变量产生数据竞争）。
	testComputeDoneHook = func() { close(done) }
	defer func() { testComputeHook = nil; testComputeDoneHook = nil }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用前就取消

	start := time.Now()
	_, err := svc.Query(ctx, "A", "B")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Query took %v to return after ctx cancelled, want near-instant", elapsed)
	}

	// 调用者取消不会打断后台那份计算（这是被测行为的设计意图：不浪费已经开始的计算），
	// 必须等它真正跑完再清理全局 hook——否则它会在下一个测试改写 hook 之后才执行，
	// 读到别的测试设置的全局状态，构成数据竞争（这是测试自身的清理时序问题，与被测代码无关）。
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background compute did not finish in time")
	}
}

func TestService_QueryFoldsConcurrentCallsForSameStationPair(t *testing.T) {
	svc := newTestService(t, geojsonFixture(t), "v1")

	var computeCount atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	testComputeHook = func() {
		computeCount.Add(1)
		once.Do(func() { close(started) })
		<-release
	}
	defer func() { testComputeHook = nil }()

	const n = 5
	results := make([][]byte, n)
	errs := make([]error, n)
	var arrived atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			arrived.Add(1) // 先报到，测试主协程据此确认所有 goroutine 都已排到调用 Query 之前
			res, err := svc.Query(context.Background(), "A", "B")
			errs[i] = err
			if err == nil {
				b, _ := json.Marshal(res)
				results[i] = b
			}
		}(i)
	}

	<-started
	// 等所有 goroutine 都已运行到「即将调用 Query」。这只保证到达 arrived.Add(1) 那一行，
	// 到真正调用 DoChan 之间仍有极小的调度窗口——但赢家此刻正卡在 testComputeHook 里等 release，
	// singleflight 的这个 key 会一直处于「进行中」状态直到我们关闭 release，所以只要在关闭前
	// 给足够余量让其余 goroutine 排到 DoChan，就不会有人错过、另起一次计算。
	deadline := time.Now().Add(5 * time.Second)
	for arrived.Load() < n && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := arrived.Load(); got < n {
		t.Fatalf("only %d/%d goroutines arrived before deadline", got, n)
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
	}
	for i := 1; i < n; i++ {
		if string(results[i]) != string(results[0]) {
			t.Errorf("goroutine %d result differs from goroutine 0", i)
		}
	}
	if computeCount.Load() != 1 {
		t.Errorf("compute ran %d times, want 1 (concurrent calls should fold into one)", computeCount.Load())
	}
}

// TestService_QueryTimesOutWithEmptyResultDoesNotCache 验证：计算超时且什么都没算出来时（这里用两个
// 完全不连通的车站，真实约束下必然无路线），Query 返回 ErrComputeTimeout 而不是把这个有歧义的空结果
// 缓存下来——之后用正常（不超时）的配置重新查询同一站点对，应该走"正常计算得出空结果"路径，
// 得到空切片 + nil error，而不是复用第一次超时时的错误状态。
func TestService_QueryTimesOutWithEmptyResultDoesNotCache(t *testing.T) {
	cfgTimeout := config.RouteConfig{
		MaxDistanceResults: 5, MaxPriceResults: 5,
		SearchWeightDistance: 0.5, SearchWeightPrice: 0.5,
		MinDirectResults: 1, MaxTransferResults: 3, TransferMinImprovement: 0.2,
		ComputeTimeoutMs: 1, // 1 毫秒：一个已经很快的查询也能被这个几乎为零的超时赶上
	}
	svc := newTestServiceWithConfig(t, geojsonDisconnectedFixture(t), "v1", cfgTimeout)

	// A、B 完全不连通，reachableIgnoringRevisit 本身求解极快（没有边可扩展），真实计时窗口很难
	// 可靠地卡在 1ms 超时前后——用 hook 在搜索真正开始前先 sleep，确定性地让 computeCtx 先过期。
	testReachabilityEntryHook = func() { time.Sleep(20 * time.Millisecond) }
	defer func() { testReachabilityEntryHook = nil }()

	_, err := svc.Query(context.Background(), "A", "B")
	if !errors.Is(err, ErrComputeTimeout) {
		t.Fatalf("first query err = %v, want ErrComputeTimeout", err)
	}
	testReachabilityEntryHook = nil

	// 换成不超时的正常配置，重新查询同一站点对：应该正常算出「无路线」的空结果，不是复用上面的超时错误。
	cfgNormal := cfgTimeout
	cfgNormal.ComputeTimeoutMs = 60_000
	svc2 := newTestServiceWithConfig(t, geojsonDisconnectedFixture(t), "v1", cfgNormal)
	results, err := svc2.Query(context.Background(), "A", "B")
	if err != nil {
		t.Fatalf("second query (normal config): unexpected error %v", err)
	}
	if len(results) != 0 {
		t.Errorf("second query results = %v, want empty (A and B are disconnected)", results)
	}
}

// TestService_QueryTimesOutWithPartialResultCachesIt 验证：计算超时但已经拿到非空部分结果时，
// Query 把这个结果当正常结果返回（不报错）并写入缓存——不会因为撞到超时就把已经找到的真实路线丢弃。
func TestService_QueryTimesOutWithPartialResultCachesIt(t *testing.T) {
	cfg := config.RouteConfig{
		MaxDistanceResults: 5, MaxPriceResults: 5,
		SearchWeightDistance: 0.5, SearchWeightPrice: 0.5,
		MinDirectResults: 1, MaxTransferResults: 3, TransferMinImprovement: 0.2,
		ComputeTimeoutMs: 1, // 1 毫秒
	}
	svc := newTestServiceWithConfig(t, geojsonFixture(t), "v1", cfg)

	// kShortest 拿到第一条（也是唯一一条）真实路径后，用远超 1ms 的 sleep 把 wall clock 推过
	// computeCtx 的截止时间，确定性构造「超时发生在已拿到非空结果之后」这个时序。
	testKShortestResultFoundHook = func() { time.Sleep(20 * time.Millisecond) }
	defer func() { testKShortestResultFoundHook = nil }()

	results, err := svc.Query(context.Background(), "A", "B")
	if err != nil {
		t.Fatalf("expected nil error for timed-out-but-non-empty result, got %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected non-empty results")
	}
	firstJSON, _ := json.Marshal(results)

	// 清掉 hook 后重新查询同一站点对：应直接命中缓存，内容与上一步完全一致。
	testKShortestResultFoundHook = nil
	cached, err := svc.Query(context.Background(), "A", "B")
	if err != nil {
		t.Fatalf("cached query: unexpected error %v", err)
	}
	cachedJSON, _ := json.Marshal(cached)
	if string(cachedJSON) != string(firstJSON) {
		t.Errorf("cached result differs from the timed-out-but-cached result:\nfirst:  %s\ncached: %s", firstJSON, cachedJSON)
	}
}
