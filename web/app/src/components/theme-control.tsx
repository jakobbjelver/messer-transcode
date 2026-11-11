import { ThemeToggle } from "@/components/ui/theme-toggle";
import { useTheme } from "@/hooks/use-theme";

/** The block plus the app's storage hook — the split the block documents. */
export function ThemeControl() {
  const { dark, toggle } = useTheme();
  return <ThemeToggle dark={dark} onToggle={toggle} />;
}
