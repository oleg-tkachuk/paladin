"use client";

import React, { createContext, useContext, useState } from "react";

interface UIContextType {
  useRelativeTime: boolean;
  setUseRelativeTime: (value: boolean) => void;
}

const UIContext = createContext<UIContextType | undefined>(undefined);

export function UIProvider({ children }: { children: React.ReactNode }) {
  const [useRelativeTime, setUseRelativeTime] = useState(() => {
    if (typeof window === "undefined") return true;
    const stored = localStorage.getItem("paladin_use_relative_time");
    return stored !== null ? stored === "true" : true;
  });

  const handleSetUseRelativeTime = (value: boolean) => {
    setUseRelativeTime(value);
    localStorage.setItem("paladin_use_relative_time", String(value));
  };

  return (
    <UIContext.Provider
      value={{
        useRelativeTime,
        setUseRelativeTime: handleSetUseRelativeTime,
      }}
    >
      {children}
    </UIContext.Provider>
  );
}

export function useUI() {
  const context = useContext(UIContext);
  if (context === undefined) {
    throw new Error("useUI must be used within a UIProvider");
  }
  return context;
}
