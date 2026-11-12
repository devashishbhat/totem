import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { router } from "@/router";
import "@/styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // The dashboard is meant to be watched while an agent is working, so
      // every view polls. The interval is what a person reads as "live"
      // without the page flickering, and the payloads are small.
      refetchInterval: 3_000,
      refetchOnWindowFocus: true,
      retry: 1,
      staleTime: 2_000,
    },
  },
});

const root = document.getElementById("root");
if (!root) throw new Error("no #root element");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
