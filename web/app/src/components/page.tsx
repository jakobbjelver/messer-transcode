import { PageContainer } from "@/components/ui/page-container";

/**
 * Every page is the same frame: one container stop (canon §16), the page gutter,
 * and vertical rhythm. No page writes its own max-width.
 */
export function Page({ children }: { children: React.ReactNode }) {
  return (
    <PageContainer size="default">
      <div className="flex flex-col gap-6 py-6 md:py-8">{children}</div>
    </PageContainer>
  );
}
