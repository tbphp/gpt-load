import { onBeforeUnmount, onMounted, onUpdated, ref, type Ref } from 'vue'

export interface SectionNavigationOptions {
  ids: Readonly<Ref<readonly string[]>>
  initialId?: string
  topOffset?: number
}

export interface SectionNavigationController {
  activeSection: Ref<string>
  selectSection(id: string): void
}

export function useSectionNavigation({
  ids,
  initialId = ids.value[0] ?? '',
  topOffset = 88,
}: SectionNavigationOptions): SectionNavigationController {
  const activeSection = ref(initialId)
  let sectionFrame = 0

  function synchronizeSection(): void {
    sectionFrame = 0
    const sectionIDs = ids.value
    const elements = sectionIDs
      .map((id) => document.getElementById(id))
      .filter((element): element is HTMLElement => element !== null)
    if (!sectionIDs.length || elements.length !== sectionIDs.length) return

    let current = elements[0]
    for (const element of elements) {
      if (element.getBoundingClientRect().top <= topOffset) current = element
      else break
    }
    const pageHeight = document.documentElement.scrollHeight
    const pageBottom = window.scrollY + window.innerHeight
    if (pageHeight > window.innerHeight + 2 && pageBottom >= pageHeight - 2)
      current = elements.at(-1) ?? current
    activeSection.value = current.id
  }

  function scheduleSynchronization(): void {
    if (sectionFrame) return
    sectionFrame = window.requestAnimationFrame(synchronizeSection)
  }

  function selectSection(id: string): void {
    activeSection.value = id
    const behavior = window.matchMedia('(prefers-reduced-motion: reduce)').matches
      ? 'auto'
      : 'smooth'
    document.getElementById(id)?.scrollIntoView({ behavior, block: 'start' })
  }

  onMounted(() => {
    window.addEventListener('scroll', scheduleSynchronization, { passive: true })
    window.addEventListener('resize', scheduleSynchronization, { passive: true })
    scheduleSynchronization()
  })

  onUpdated(scheduleSynchronization)

  onBeforeUnmount(() => {
    window.removeEventListener('scroll', scheduleSynchronization)
    window.removeEventListener('resize', scheduleSynchronization)
    if (sectionFrame) window.cancelAnimationFrame(sectionFrame)
  })

  return { activeSection, selectSection }
}
