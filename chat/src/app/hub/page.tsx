import type {Metadata} from "next";
import {Suspense} from "react";
import {HubView} from "@/components/hub-view";

const basePath = (process.env.NEXT_PUBLIC_BASE_PATH ?? "/chat").replace(/\/$/, "");

// The hub's page installs as an app (Add to Home Screen), which is where
// an iPhone gets its pushes: the manifest and icons are in public/hub/.
export const metadata: Metadata = {
  appleWebApp: {capable: true, title: "Tendyard Hub", statusBarStyle: "default"},
  icons: {apple: `${basePath}/hub/apple-touch-icon.png`},
};

export default function HubPage() {
  return (
    <>
      {/* With the page's cookies: behind a sign-in of its own (a Coder
          workspace app) the manifest isn't served without them, and a
          manifest link fetches without them unless it asks. React puts it
          in the head. */}
      <link rel="manifest" href={`${basePath}/hub/manifest.json`} crossOrigin="use-credentials" />
      <Suspense fallback={<div className="p-4 text-center text-sm">Loading the hub...</div>}>
        <HubView />
      </Suspense>
    </>
  );
}
