// Shared wording for sizes and times, so every page says them the same way.

export const bytes = (b: number) =>
  b >= 1 << 30 ? `${(b / (1 << 30)).toFixed(1)} GB` : b >= 1 << 20 ? `${Math.round(b / (1 << 20))} MB` : b >= 1024 ? `${Math.round(b / 1024)} KB` : `${b} B`;

// unix seconds -> "just now", "12 min ago", "3 h ago", "4 days ago"
export const ago = (unix?: number) => {
  if (!unix) return "never";
  const s = Date.now() / 1000 - unix;
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} days ago`;
};
