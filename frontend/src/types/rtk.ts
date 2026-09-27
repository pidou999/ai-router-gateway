export interface RTKStats {
  total_compressions: number;
  total_saved_tokens: number;
  filter_usage: Record<string, number>;
  recent_compressions: RecentCompression[];
}

export interface RecentCompression {
  filter: string;
  saved_tokens: number;
  original_size: number;
  compressed_size: number;
  timestamp: string;
}
