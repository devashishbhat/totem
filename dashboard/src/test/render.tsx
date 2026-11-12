/** Renders a component inside the providers the app gives it. */

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";

/**
 * Mounts `ui` at `path`, with the app's other routes registered so that links
 * to them resolve. Returns what @testing-library's render returns.
 */
export function renderAt(ui: ReactNode, path = "/") {
  const rootRoute = createRootRoute();
  const target = createRoute({
    getParentRoute: () => rootRoute,
    path,
    component: () => ui,
  });
  const others = [
    "/decisions/$decisionId",
    "/memories/$memoryId",
    "/sources/$sourceId",
    "/runs/$runId",
    "/runs",
    "/",
  ]
    .filter((other) => other !== path)
    .map((other) =>
      createRoute({ getParentRoute: () => rootRoute, path: other, component: () => null }),
    );

  const router = createRouter({
    routeTree: rootRoute.addChildren([target, ...others]),
    history: createMemoryHistory({ initialEntries: [concrete(path)] }),
  });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
      <RouterProvider router={router as any} />
    </QueryClientProvider>,
  );
}

/** Fills route parameters with placeholder values. */
function concrete(path: string): string {
  return path.replace(/\$[A-Za-z]+/g, "x");
}
