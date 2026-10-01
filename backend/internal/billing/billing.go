package billing

import (
	"errors"
	"math"
	"time"
	"vpn/backend/internal/model"
)

// One unit = 1/1000 byte. 1000 permille = 1x, 500 permille = 0.5x.
// Integer units preserve fractions across reports; never round every packet up.
func ChargeUnits(rawBytes, permille int64) (int64, error) {
	if rawBytes < 0 || permille <= 0 || permille > 10000 || rawBytes > math.MaxInt64/permille {
		return 0, errors.New("invalid or overflowing traffic counter")
	}
	return rawBytes * permille, nil
}
func Eligible(s *model.Subscription, now time.Time, allowTest bool) bool {
	return s != nil && now.Before(s.ExpiresAt) && !now.Before(s.StartsAt) && s.TrafficLimitBytes > 0 && s.TrafficLimitBytes <= math.MaxInt64/1000 && s.UsedUnits < s.TrafficLimitBytes*1000 && (!s.IsTest || allowTest)
}
