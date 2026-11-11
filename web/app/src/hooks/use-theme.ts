import { useCallback, useState } from "react";

const KEY = "messer-theme";

/**
 * The app's 20-line theme hook: canon switches on `.dark` at <html>, and the
 * inline script in index.html applies the stored choice before first paint. This
 * is the only writer of the key; the ThemeToggle block stays presentational.
 */
export function useTheme() {
  const [dark, setDark] = useState(
    () => typeof document !== "undefined" && document.documentElement.classList.contains("dark"),
  );

  const toggle = useCallback(() => {
    setDark((current) => {
      const next = !current;
      document.documentElement.classList.toggle("dark", next);
      try {
        localStorage.setItem(KEY, next ? "dark" : "light");
      } catch {
        // Storage blocked: the class still switches for this session.
      }
      return next;
    });
  }, []);

  return { dark, toggle };
}
