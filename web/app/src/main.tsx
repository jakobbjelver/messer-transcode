import { StrictMode, lazy, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router-dom";

import "./index.css";
import { AppChrome } from "@/components/app-chrome";
import { StreamProvider } from "@/components/stream-provider";
import { FilePage } from "@/pages/file";
import { NotFoundPage } from "@/pages/not-found";
import { OverviewPage } from "@/pages/overview";
import { QueuePage } from "@/pages/queue";
import { RunsPage } from "@/pages/runs";
import { SettingsPage } from "@/pages/settings";

// The conformance sink is a dev-only surface. `import.meta.env.DEV` is replaced
// with false in a production build, so the branch and its chunk are dropped —
// `grep -c sink dist/assets/*.js` must return nothing.
const Sink = import.meta.env.DEV ? lazy(() => import("@/dev/sink").then((m) => ({ default: m.Sink }))) : null;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <StreamProvider>
        <AppChrome>
          <Routes>
            <Route path="/" element={<OverviewPage />} />
            <Route path="/queue" element={<QueuePage />} />
            <Route path="/files/:id" element={<FilePage />} />
            <Route path="/runs" element={<RunsPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            {Sink ? (
              <Route
                path="/ui"
                element={
                  <Suspense fallback={null}>
                    <Sink />
                  </Suspense>
                }
              />
            ) : null}
            <Route path="*" element={<NotFoundPage />} />
          </Routes>
        </AppChrome>
      </StreamProvider>
    </BrowserRouter>
  </StrictMode>,
);
