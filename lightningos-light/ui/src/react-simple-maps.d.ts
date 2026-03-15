declare module 'react-simple-maps' {
  import type { ComponentType, ReactNode, SVGProps } from 'react'

  export const ComposableMap: ComponentType<any>
  export const Geographies: ComponentType<any>
  export const Geography: ComponentType<any>
  export const Marker: ComponentType<any>
  export const Sphere: ComponentType<any>
  export const ZoomableGroup: ComponentType<any>
  export function useMapContext(): {
    projection: (coordinates: [number, number]) => [number, number] | null
  }
}
