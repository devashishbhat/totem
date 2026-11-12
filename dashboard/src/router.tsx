import { createRootRoute, createRoute, createRouter, type AnyRoute } from "@tanstack/react-router";

import { DecisionPage } from "@/routes/DecisionPage";
import { MemoryPage } from "@/routes/MemoryPage";
import { Overview } from "@/routes/Overview";
import { Root } from "@/routes/Root";
import { RunTimeline } from "@/routes/RunTimeline";
import { Runs } from "@/routes/Runs";
import { SourcePage } from "@/routes/SourcePage";

/**
 * The routes, defined in code rather than generated from the filesystem: the
 * app has five pages and one list of them is easier to follow than a build
 * step. See docs/decisions.md.
 */
const rootRoute = createRootRoute({ component: Root });

const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: "/", component: Overview });
const runsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runs",
  component: Runs,
});
const runRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/runs/$runId",
  component: RunTimeline,
});
const decisionRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/decisions/$decisionId",
  component: DecisionPage,
});
const memoryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/memories/$memoryId",
  component: MemoryPage,
});
const sourceRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/sources/$sourceId",
  component: SourcePage,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  runsRoute,
  runRoute,
  decisionRoute,
  memoryRoute,
  sourceRoute,
] as AnyRoute[]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
