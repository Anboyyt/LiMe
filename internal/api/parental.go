package api

import "lime/internal/store"

// maxRatingOf 返回用户的家长分级上限；0 表示不限制。
func maxRatingOf(user *store.User) int {
	if user != nil && user.MaxParentalRating > 0 {
		return user.MaxParentalRating
	}
	return 0
}

// applyRatingLimit 把用户的最高允许分级注入列表查询。
func applyRatingLimit(user *store.User, q *store.ItemQuery) {
	if user != nil && user.MaxParentalRating > 0 {
		q.MaxRating = user.MaxParentalRating
	}
}

// ratingAllowed 判断用户能否看到条目。未分级（0）默认放行，
// 否则刮削没跑完的库会对受限账号整库不可见。
func ratingAllowed(user *store.User, it *store.Item) bool {
	if user == nil || user.MaxParentalRating <= 0 {
		return true
	}
	return it.RatingScore == 0 || it.RatingScore <= user.MaxParentalRating
}

// filterByRating 用于播放历史这类已经取回内存的列表。
func filterByRating(user *store.User, items []*store.Item) []*store.Item {
	if user == nil || user.MaxParentalRating <= 0 {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if ratingAllowed(user, it) {
			out = append(out, it)
		}
	}
	return out
}
