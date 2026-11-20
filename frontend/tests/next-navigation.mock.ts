import { vi } from "vitest";
import { navState, routerMock } from "./utils";

vi.mock("next/navigation", () => ({
  useRouter: () => routerMock,
  usePathname: () => navState.pathname,
  useSearchParams: () => navState.search,
  useParams: () => ({}),
  redirect: vi.fn(),
  notFound: vi.fn(),
}));
