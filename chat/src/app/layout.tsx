import type { Metadata, Viewport } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "@xterm/xterm/css/xterm.css";
import "./globals.css";
import "./hljs-theme.css";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ThemeProvider } from "@/components/theme-provider";
import { ViewportGuard } from "@/components/viewport-guard";
import {defaultLocale, uiCopy} from "@/lib/ui-copy";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

// The tab title is rendered by ChatProvider: it follows the agent's state.
export const metadata: Metadata = {
  description: uiCopy.metadata.description,
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  // Where supported, the keyboard shrinks the page instead of sliding it up.
  interactiveWidget: "resizes-content",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    // The font variables go on <html>: Tailwind sets font-family there, and
    // a variable defined only on <body> left the page in the system font.
    <html lang={defaultLocale} className={`${geistSans.variable} ${geistMono.variable}`} suppressHydrationWarning>
      <body className="antialiased">
        <ThemeProvider
          attribute="class"
          defaultTheme="system"
          enableSystem
          disableTransitionOnChange
        >
          <TooltipProvider delayDuration={300}>
            {children}
          </TooltipProvider>
          <Toaster richColors closeButton />
          <ViewportGuard />
        </ThemeProvider>
      </body>
    </html>
  );
}
