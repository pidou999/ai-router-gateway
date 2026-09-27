# RTK Statistics Implementation

## Overview
RTK compression system tracks statistics for monitoring token savings.

## Files
- `backend/internal/compressor/stats.go` - Global stats tracker (thread-safe)
- `backend/internal/handlers/rtk_stats_handler.go` - API endpoint
- `frontend/src/types/rtk.ts` - TypeScript types

## API
```
GET /api/admin/rtk/stats
Response: { success, data: { total_compressions, total_saved_tokens, filter_usage, recent_compressions } }
```

## Key Points
- Statistics recorded via `RecordCompression(filterName, origTokens, compTokens)`
- Thread-safe with `sync.RWMutex`
- Frontend auto-refreshes every 30s when RTK enabled
- Reset stats by restarting gateway (in-memory only)

## Testing
```bash
curl http://localhost:5176/api/admin/rtk/stats -H "Authorization: Bearer <token>"
```
