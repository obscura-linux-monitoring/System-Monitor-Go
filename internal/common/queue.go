package common

import (
	"sync"
)

// Queue 스레드 안전한 큐 인터페이스
type Queue interface {
	Push(item interface{})
	Pop() interface{}
	TryPop() (interface{}, bool)
	Size() int
}

// ThreadSafeQueue 스레드 안전한 큐 구현체
type ThreadSafeQueue struct {
	items []interface{}
	mutex sync.Mutex
	cond  *sync.Cond
	size  int
}

// NewQueue 새 큐 생성
func NewQueue(capacity int) Queue {
	q := &ThreadSafeQueue{
		items: make([]interface{}, 0, capacity),
		size:  capacity,
	}
	q.cond = sync.NewCond(&q.mutex)
	return q
}

// Push 아이템을 큐에 추가
func (q *ThreadSafeQueue) Push(item interface{}) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	// 큐가 가득 차면 가장 오래된 항목을 제거
	if len(q.items) >= q.size {
		q.items = q.items[1:]
	}

	q.items = append(q.items, item)
	q.cond.Signal() // 대기 중인 Pop 호출자에게 신호 전송
}

// Pop 큐에서 아이템 제거 (아이템이 없으면 대기)
func (q *ThreadSafeQueue) Pop() interface{} {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	// 큐가 비어있으면 대기
	for len(q.items) == 0 {
		q.cond.Wait()
	}

	item := q.items[0]
	q.items = q.items[1:]
	return item
}

// TryPop 아이템 가져오기 시도 (비어있으면 false 반환)
func (q *ThreadSafeQueue) TryPop() (interface{}, bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if len(q.items) == 0 {
		return nil, false
	}

	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// Size 큐의 현재 크기 반환
func (q *ThreadSafeQueue) Size() int {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	return len(q.items)
}
